// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

type mfaChallengePolicy struct {
	cloud cloud.Configuration
}

func NewMfaChallengePolicy(cloud cloud.Configuration) policy.Policy {
	return &mfaChallengePolicy{
		cloud: cloud,
	}
}

func (p *mfaChallengePolicy) Do(req *policy.Request) (*http.Response, error) {
	resp, err := req.Next()

	if resp.StatusCode == http.StatusUnauthorized {
		authHeader, ok := resp.Header["Www-Authenticate"]
		if ok {
			for _, val := range authHeader {
				challenges := ParseWwwAuthenticateHeader(val)
				for _, challenge := range challenges {
					if isEntraBearerChallenge(challenge, p.cloud.ActiveDirectoryAuthorityHost) {
						if err := saveClaims(challenge); err != nil {
							return resp, err
						}

						err := ReLoginRequiredError{
							scenario: "reauthentication required",
							errText:  "mfa challenge",
							loginCmd: "azd auth login",
						}
						suggestion := fmt.Sprintf("Suggestion: %s, run `%s` to acquire a new token.", err.scenario, err.loginCmd)
						if err.helpLink != "" {
							suggestion += fmt.Sprintf(" See %s for more info.", err.helpLink)
						}
						return resp, &internal.ErrorWithSuggestion{
							Err:        &err,
							Suggestion: suggestion,
						}
					}
				}
			}
		}
	}

	return resp, err
}

func isEntraBearerChallenge(challenge AuthChallenge, entraHost string) bool {
	if challenge.Scheme != "Bearer" {
		return false
	}

	// check for 'authorization_uri' matching entra's host, i.e. 'https://login.microsoftonline.com'
	uri := challenge.AuthParams["authorization_uri"]
	return strings.HasPrefix(uri, entraHost)
}

func claimsFilePath() (string, error) {
	configDir, err := config.GetUserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get config dir: %w", err)
	}

	return filepath.Join(configDir, "auth.claims"), nil
}

func saveClaims(challenge AuthChallenge) error {
	claims := challenge.AuthParams["claims"]
	claimsJson, err := base64.StdEncoding.DecodeString(claims)
	if err != nil {
		return fmt.Errorf("decoding claims: %w", err)
	}

	claimsFile, err := claimsFilePath()
	if err != nil {
		return err
	}

	if err := os.WriteFile(claimsFile, claimsJson, osutil.PermissionFileOwnerOnly); err != nil {
		return fmt.Errorf("failed to save claims: %w", err)
	}

	return nil
}

// AuthChallenge represents an authentication challenge parsed from a WWW-Authenticate header.
// It contains the scheme, optional token68 (legacy), and any additional authentication parameters.
//
// See https://datatracker.ietf.org/doc/html/rfc9110#name-www-authenticate for more details.
type AuthChallenge struct {
	// Scheme is the authentication scheme, e.g. "Bearer" or "Basic".
	Scheme string

	// Token68 is a legacy field for token-based challenges, e.g. "eyJhbGciOaJIUzI1NiJ9".
	Token68 string

	// AuthParams contains additional parameters for the challenge, e.g. realm, error, etc.
	AuthParams map[string]string
}

// An element of a comma-separated list in a WWW-Authenticate header.
type element struct {
	// Optional scheme name, e.g. "Bearer" or "Basic".
	Scheme string

	// Optional key, e.g. "realm" or "error" in `Bearer realm="example.com", error="invalid_token"`.
	Key string

	// Token68 or value, e.g. "eyJhbGciOaJIUzI1NiJ9" in `Bearer eyJhbGciOaJIUzI1NiJ9`,
	// or "invalid_token" in `error="invalid_token"`.
	Value string
}

// ParseWwwAuthenticateHeader parses a WWW-Authenticate header value into a slice of AuthChallenge structs.
func ParseWwwAuthenticateHeader(headerValue string) []AuthChallenge {
	var challenges []AuthChallenge
	parts := splitComma(headerValue)

	for _, part := range parts {
		elem, err := parseCommaElement(part)

		if err != nil {
			log.Printf("parsing Www-Authenticate header: %s", err)
		} else if elem.Scheme != "" {
			// handle a new auth-scheme challenge
			authChallenge := AuthChallenge{
				Scheme: elem.Scheme,
			}

			if elem.Key != "" {
				if authChallenge.AuthParams == nil {
					authChallenge.AuthParams = make(map[string]string)
				}

				authChallenge.AuthParams[elem.Key] = elem.Value
			} else {
				authChallenge.Token68 = elem.Value
			}

			challenges = append(challenges, authChallenge)
		} else if len(challenges) > 0 {
			// handle continuation of previous challenge
			challenges[len(challenges)-1].AuthParams[elem.Key] = elem.Value
		}
	}

	return challenges
}

// parseCommaElement parses a single comma-delimited element from a Www-Authenticate header.
//
// It handles three possible formats of a challenge element:
// - `Bearer realm="example.com"`  -> (scheme, key, value)
// - `Bearer eyJhbGciOaJIUzI1NiJ9`  -> (scheme, value)
// - `error="invalid_token"`  -> (key, value)
//
// It returns an element struct with the parsed values.
func parseCommaElement(s string) (element, error) {
	before, after, hasEquals := strings.Cut(s, "=")
	if !hasEquals {
		schemeToken68 := strings.Fields(before)
		if len(schemeToken68) == 2 {
			return element{
				Scheme: schemeToken68[0],
				Value:  schemeToken68[1],
			}, nil
		} else {
			return element{}, fmt.Errorf("unexpected elements in '%s'", s)
		}
	}

	schemeAndKey := strings.Fields(before)

	value := strings.TrimSpace(after)
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		inner := value[1 : len(value)-1]
		value = handleEscapes(inner)
	}

	if len(schemeAndKey) == 1 {
		return element{
			Key:   schemeAndKey[0],
			Value: value,
		}, nil
	}

	if len(schemeAndKey) == 2 {
		return element{
			Scheme: schemeAndKey[0],
			Key:    schemeAndKey[1],
			Value:  value,
		}, nil
	}

	return element{}, fmt.Errorf("unexpected elements in '%s'", s)
}

// handleEscapes processes the inner value of a quoted string by interpreting escaped characters.
func handleEscapes(s string) string {
	escaped := false
	var value strings.Builder

	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			value.WriteByte(c)
			escaped = false
			continue
		}

		switch c {
		case '\\':
			escaped = true
		default:
			value.WriteByte(c)
		}
	}

	return value.String()
}

// Split by commas outside of quoted strings.
func splitComma(value string) []string {
	var elements []string
	var current strings.Builder
	inQuote := false

	for i := 0; i < len(value); i++ {
		c := value[i]

		switch c {
		case '"':
			if inQuote && i-1 >= 0 && value[i-1] == '\\' {
				current.WriteByte(c)
				continue
			}
			inQuote = !inQuote
		case ',':
			if !inQuote {
				elements = append(elements, current.String())
				current.Reset()
				continue
			}
		}

		current.WriteByte(c)
	}

	if current.Len() > 0 {
		elements = append(elements, current.String())
	}

	return elements
}
