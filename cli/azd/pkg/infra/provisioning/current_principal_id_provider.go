// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/pkg/account"
	"github.com/azure/azure-dev/pkg/auth"
	"github.com/azure/azure-dev/pkg/azapi"
	"github.com/azure/azure-dev/pkg/azureutil"
	"github.com/azure/azure-dev/pkg/environment"
)

type CurrentPrincipalIdProvider interface {
	// CurrentPrincipalId returns the object id of the current logged in principal, or an error if it can not be
	// determined.
	CurrentPrincipalId(ctx context.Context) (string, error)
	CurrentPrincipalType(ctx context.Context) (PrincipalType, error)
}

func NewPrincipalIdProvider(
	env *environment.Environment,
	userProfileService *azapi.UserProfileService,
	subResolver account.SubscriptionTenantResolver,
	authManager *auth.Manager,
) CurrentPrincipalIdProvider {
	return &principalIDProvider{
		env:                env,
		userProfileService: userProfileService,
		subResolver:        subResolver,
		authManager:        authManager,
	}
}

type principalIDProvider struct {
	env                *environment.Environment
	userProfileService *azapi.UserProfileService
	subResolver        account.SubscriptionTenantResolver
	authManager        *auth.Manager
}

func (p *principalIDProvider) CurrentPrincipalId(ctx context.Context) (string, error) {
	tenantId, err := p.subResolver.LookupTenant(ctx, p.env.GetSubscriptionId())
	if err != nil {
		return "", fmt.Errorf("getting tenant id for subscription %s. Error: %w", p.env.GetSubscriptionId(), err)
	}

	principalId, err := azureutil.GetCurrentPrincipalId(ctx, p.userProfileService, tenantId)
	if err != nil {
		return "", fmt.Errorf("fetching current user information: %w", err)
	}

	return principalId, nil
}

const (
	UserType             PrincipalType = "User"
	ServicePrincipalType PrincipalType = "ServicePrincipal"
)

type PrincipalType string

func (p *principalIDProvider) CurrentPrincipalType(ctx context.Context) (PrincipalType, error) {
	loginDetails, err := p.authManager.LogInDetails(ctx)
	if err != nil {
		return "", fmt.Errorf("fetching login details: %w", err)
	}

	principalType := UserType
	if loginDetails.LoginType == auth.ClientIdLoginType {
		principalType = ServicePrincipalType
	}

	return principalType, nil
}
