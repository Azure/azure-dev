// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azureutil

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
)

// GetCurrentPrincipalId returns the object id of the current
// principal authenticated with the CLI
// (via ad sp signed-in-user), falling back to extracting the
// `oid` claim from an access token a principal can not be
// obtained in this way.
func GetCurrentPrincipalId(ctx context.Context, userProfile *azapi.UserProfileService, tenantId string) (string, error) {
	principalId, err := userProfile.GetSignedInUserId(ctx, tenantId)
	if err == nil {
		return principalId, nil
	}

	token, err := userProfile.GetAccessToken(ctx, tenantId)
	if err != nil {
		return "", fmt.Errorf("getting access token: %w", err)
	}

	oid, err := auth.GetOidFromAccessToken(token.AccessToken)
	if err != nil {
		return "", fmt.Errorf("getting oid from token: %w", err)
	}

	return oid, nil
}
