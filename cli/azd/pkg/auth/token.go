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

// TokenClaims contains claims about a user.
// https://docs.microsoft.com/azure/active-directory/develop/id-tokens .
type TokenClaims struct {
	PreferredUsername string `json:"preferred_username,omitempty"`
	GivenName         string `json:"given_name,omitempty"`
	FamilyName        string `json:"family_name,omitempty"`
	MiddleName        string `json:"middle_name,omitempty"`
	Name              string `json:"name,omitempty"`
	Oid               string `json:"oid,omitempty"`
	TenantId          string `json:"tid,omitempty"`
	Subject           string `json:"sub,omitempty"`
	UPN               string `json:"upn,omitempty"`
	Email             string `json:"email,omitempty"`
	AlternativeID     string `json:"alternative_id,omitempty"`
	Issuer            string `json:"iss,omitempty"`
	Audience          string `json:"aud,omitempty"`
	ExpirationTime    int64  `json:"exp,omitempty"`
	IssuedAt          int64  `json:"iat,omitempty"`
	NotBefore         int64  `json:"nbf,omitempty"`
}

func GetTenantIdFromToken(token string) (string, error) {
	claims, err := GetClaimsFromAccessToken(token)
	if err != nil {
		return "", err
	}

	if claims.TenantId == "" {
		return "", errors.New("no tid claim")
	}

	return claims.TenantId, nil
}

// GetOidFromAccessToken extracts a string claim with the name "oid" from an access token.
// Access Tokens are JWT and the middle component is a base64 encoded string of a JSON object
// with claims.
func GetOidFromAccessToken(token string) (string, error) {
	claims, err := GetClaimsFromAccessToken(token)
	if err != nil {
		return "", err
	}

	if claims.Oid == "" {
		return "", errors.New("no oid claim")
	}

	return claims.Oid, nil
}

// GetClaimsFromAccessToken extracts claims from an access token.
// Access Tokens are JWT and the middle component is a base64 encoded string of a JSON object
// with claims.
func GetClaimsFromAccessToken(token string) (TokenClaims, error) {
	matches := jwtClaimsRegex.FindStringSubmatch(token)
	if len(matches) != 2 {
		return TokenClaims{}, errors.New("malformed access token")
	}

	bytes, err := base64.RawURLEncoding.DecodeString(matches[1])
	if err != nil {
		return TokenClaims{}, err
	}

	var claims TokenClaims
	if err := json.Unmarshal(bytes, &claims); err != nil {
		return TokenClaims{}, err
	}

	return claims, nil
}
