// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azureutil

import (
	"context"
	"errors"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
)

// GetCurrentPrincipalId returns the object ID of the current principal authenticated with the CLI.
// It prefers the oid claim from an ARM access token, falling back to Graph /me when acquiring the
// token fails or when the token does not include a usable oid.
func GetCurrentPrincipalId(ctx context.Context, userProfile *azapi.UserProfileService, tenantId string) (string, error) {
	token, err := userProfile.GetAccessToken(ctx, tenantId)
	if err == nil {
		oid, oidErr := auth.GetOidFromAccessToken(token.AccessToken)
		if oidErr == nil {
			return oid, nil
		}

		err = fmt.Errorf("getting oid from token: %w", oidErr)
	} else {
		err = fmt.Errorf("getting access token: %w", err)
	}

	principalId, graphErr := userProfile.GetSignedInUserId(ctx, tenantId)
	if graphErr == nil {
		return principalId, nil
	}

	return "", fmt.Errorf(
		"resolving current principal ID from token oid and Graph fallback: %w",
		errors.Join(err, fmt.Errorf("getting signed-in user id: %w", graphErr)),
	)
}
