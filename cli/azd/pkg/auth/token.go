package auth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
)

// cspell: disable

// jwtClaimsRegex is a regular expression for JWT. A JWT is a string with three base64 encoded
// components (using the "url safe" base64 alphabet) separated by dots.  For example:
// eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c
//
//nolint:lll
var jwtClaimsRegex = regexp.MustCompile(`^[a-zA-Z0-9-_]*\.([a-zA-Z0-9-_]*)\.[a-zA-Z0-9-_]*$`)

// cspell: enable

func GetTenantIdFromToken(token string) (string, error) {
	return getTidClaimFromAccessToken(token)
}

// GetOidFromAccessToken extracts a string claim with the name "oid" from an access token.
// Access Tokens are JWT and the middle component is a base64 encoded string of a JSON object
// with claims.
func GetOidFromAccessToken(token string) (string, error) {
	matches := jwtClaimsRegex.FindStringSubmatch(token)
	if len(matches) != 2 {
		return "", errors.New("malformed access token")
	}

	bytes, err := base64.RawURLEncoding.DecodeString(matches[1])
	if err != nil {
		return "", err
	}

	var claims struct {
		Oid *string
	}

	if err := json.Unmarshal(bytes, &claims); err != nil {
		return "", err
	}

	if claims.Oid == nil {
		return "", errors.New("no oid claim")
	}

	return *claims.Oid, nil
}

func getTidClaimFromAccessToken(token string) (string, error) {
	matches := jwtClaimsRegex.FindStringSubmatch(token)
	if len(matches) != 2 {
		return "", errors.New("malformed access token")
	}

	bytes, err := base64.RawURLEncoding.DecodeString(matches[1])
	if err != nil {
		return "", err
	}

	var claims struct {
		Tid *string
	}

	if err := json.Unmarshal(bytes, &claims); err != nil {
		return "", err
	}

	if claims.Tid == nil {
		return "", errors.New("no tid claim")
	}

	return *claims.Tid, nil
}
