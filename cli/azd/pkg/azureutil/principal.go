// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azureutil

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

// GetCurrentPrincipalId returns the object id of the current
// principal authenticated with the CLI
// (via ad sp signed-in-user), falling back to extracting the
// `oid` claim from an access token a principal can not be
// obtained in this way.
func GetCurrentPrincipalId(ctx context.Context, azCli azcli.AzCli) (*string, error) {
	principalId, err := azCli.GetSignedInUserId(ctx)
	if err == nil {
		return principalId, nil
	}

	token, err := azCli.GetAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting access token: %w", err)
	}

	oid, err := getOidClaimFromAccessToken(token.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("getting oid from token: %w", err)
	}

	return &oid, nil
}

// cspell: disable

// jwtClaimsRegex is a regular expression for JWT. A JWT is a string with three base64 encoded
// components (using the "url safe" base64 alphabet) separated by dots.  For example:
// eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c
//
//nolint:lll
var jwtClaimsRegex = regexp.MustCompile(`^[a-zA-Z0-9-_]*\.([a-zA-Z0-9-_]*)\.[a-zA-Z0-9-_]*$`)

// cspell: enable

// getOidClaimFromAccessToken extracts a string claim with the name "oid" from an access token.
// Access Tokens are JWT and the middle component is a base64 encoded string of a JSON object
// with claims.
func getOidClaimFromAccessToken(token string) (string, error) {
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
