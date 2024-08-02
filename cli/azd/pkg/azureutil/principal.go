// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azureutil

import (
	"context"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

// GetCurrentPrincipalId returns the object id of the current
// principal authenticated with the CLI
// (via ad sp signed-in-user), falling back to extracting the
// `oid` claim from an access token a principal can not be
// obtained in this way.
func GetCurrentPrincipalId(ctx context.Context, userProfile *azcli.UserProfileService, tenantId string) (string, error) {
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

type LoggedInPrincipalProfileData struct {
	PrincipalId        string
	PrincipalType      string
	PrincipalLoginName string
}

// LoggedInPrincipalProfile returns the info about the current logged in principal
func LoggedInPrincipalProfile(
	ctx context.Context, userProfile *azcli.UserProfileService, tenantId string) (*LoggedInPrincipalProfileData, error) {
	principalProfile, err := userProfile.SignedProfile(ctx, tenantId)
	if err == nil {
		return &LoggedInPrincipalProfileData{
			PrincipalId:        principalProfile.Id,
			PrincipalType:      "User",
			PrincipalLoginName: principalProfile.UserPrincipalName,
		}, nil
	}

	token, err := userProfile.GetAccessToken(ctx, tenantId)
	if err != nil {
		return nil, fmt.Errorf("getting access token: %w", err)
	}

	tokenClaims, err := auth.GetClaimsFromAccessToken(token.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("getting oid from token: %w", err)
	}

	appProfile, err := userProfile.AppProfile(ctx, tenantId)
	if err == nil {
		return &LoggedInPrincipalProfileData{
			PrincipalId:        *appProfile.AppId,
			PrincipalType:      "Application",
			PrincipalLoginName: appProfile.DisplayName,
		}, nil
	} else {
		log.Println(fmt.Errorf("fetching current user information: %w", err))
	}

	return &LoggedInPrincipalProfileData{
		PrincipalId:        tokenClaims.LocalAccountId(),
		PrincipalType:      "User",
		PrincipalLoginName: tokenClaims.Email,
	}, nil
}
