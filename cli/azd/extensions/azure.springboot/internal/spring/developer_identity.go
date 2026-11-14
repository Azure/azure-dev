// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

type developerIdentity struct {
	ObjectID           string
	UserAccessTenantID string
}

type developerIdentityResolver interface {
	Resolve(context.Context, string) (*developerIdentity, error)
}

type userAccessTenantResolver interface {
	Resolve(context.Context, string) (string, error)
}

type azureDeveloperIdentityResolver struct {
	client  *azdext.AzdClient
	tenants userAccessTenantResolver
}

func newDeveloperIdentityResolver(client *azdext.AzdClient) developerIdentityResolver {
	return &azureDeveloperIdentityResolver{
		client:  client,
		tenants: newUserAccessTenantResolver(client),
	}
}

type azureUserAccessTenantResolver struct {
	client *azdext.AzdClient
}

func newUserAccessTenantResolver(client *azdext.AzdClient) userAccessTenantResolver {
	return &azureUserAccessTenantResolver{client: client}
}

func (r *azureUserAccessTenantResolver) Resolve(ctx context.Context, subscriptionID string) (string, error) {
	tenant, err := r.client.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
		SubscriptionId: subscriptionID,
	})
	if err != nil {
		return "", fmt.Errorf("looking up the developer user access tenant: %w", err)
	}
	userTenantID := strings.TrimSpace(tenant.GetTenantId())
	if userTenantID == "" {
		return "", errorsNoTenant()
	}
	return userTenantID, nil
}

func (r *azureDeveloperIdentityResolver) Resolve(
	ctx context.Context,
	subscriptionID string,
) (*developerIdentity, error) {
	principal, err := r.client.Account().GetCurrentPrincipal(ctx, &azdext.GetCurrentPrincipalRequest{
		SubscriptionId: subscriptionID,
	})
	if err != nil {
		return nil, fmt.Errorf("resolving the current developer principal: %w", err)
	}
	if principal.GetPrincipalType() != azdext.PrincipalType_PRINCIPAL_TYPE_USER {
		return nil, validationError(
			"managed_identity_requires_developer_user",
			"managedidentity database setup requires the current azd principal to be a user",
			"run azd auth login with a User account that can provision the selected subscription",
		)
	}
	objectID := strings.TrimSpace(principal.GetObjectId())
	if !validAdministratorObjectID(objectID) {
		return nil, validationError(
			"missing_developer_object_id",
			"azd did not return the current developer object ID in the subscription tenant",
			"run azd auth login again and verify access to the selected subscription",
		)
	}

	userTenantID, err := r.tenants.Resolve(ctx, subscriptionID)
	if err != nil {
		return nil, err
	}

	return &developerIdentity{
		ObjectID:           objectID,
		UserAccessTenantID: userTenantID,
	}, nil
}

func validAdministratorObjectID(value string) bool {
	return databasePrincipalIDPattern.MatchString(value) && value != "00000000-0000-0000-0000-000000000000"
}

func validAdministratorName(value string) bool {
	if strings.TrimSpace(value) == "" || len(value) > 256 {
		return false
	}
	return !strings.ContainsFunc(value, func(char rune) bool {
		return char == 0 || unicode.IsControl(char)
	})
}
