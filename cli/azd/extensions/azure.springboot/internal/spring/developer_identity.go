// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// #nosec G101 -- public OAuth scope, not a credential.
const armTokenScope = "https://management.azure.com/.default"

type developerIdentity struct {
	ObjectID           string
	AdministratorName  string
	UserAccessTenantID string
}

type developerIdentityResolver interface {
	Resolve(context.Context, string, string) (*developerIdentity, error)
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
	administratorNameOverride string,
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
			"run azd auth login as the developer who will be configured as the PostgreSQL Entra administrator",
		)
	}
	objectID := strings.TrimSpace(principal.GetObjectId())
	if objectID == "" {
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

	administratorName := strings.TrimSpace(administratorNameOverride)
	if administratorName == "" {
		credential, err := azidentity.NewAzureDeveloperCLICredential(
			&azidentity.AzureDeveloperCLICredentialOptions{
				TenantID:                   userTenantID,
				AdditionallyAllowedTenants: []string{"*"},
			},
		)
		if err != nil {
			return nil, fmt.Errorf("creating the developer azd credential: %w", err)
		}
		token, err := credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{armTokenScope}})
		if err != nil {
			return nil, fmt.Errorf("acquiring current developer identity metadata: %w", err)
		}
		administratorName, err = administratorNameFromToken(token.Token)
		if err != nil {
			return nil, validationError(
				"missing_database_administrator_name",
				"the current azd login did not expose a PostgreSQL administrator login name",
				"set database.auth.administratorName to the current developer's Entra login name",
			)
		}
	}
	if !validAdministratorName(administratorName) {
		return nil, validationError(
			"invalid_database_administrator_name",
			"the resolved PostgreSQL administrator login name contains unsupported characters",
			"set database.auth.administratorName to the current developer's Entra login name",
		)
	}
	return &developerIdentity{
		ObjectID:           objectID,
		AdministratorName:  administratorName,
		UserAccessTenantID: userTenantID,
	}, nil
}

func administratorNameFromToken(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("access token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decoding access token metadata: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("decoding access token claims: %w", err)
	}
	for _, name := range []string{"preferred_username", "upn", "unique_name"} {
		value, _ := claims[name].(string)
		value = strings.TrimSpace(value)
		if validAdministratorName(value) {
			return value, nil
		}
	}
	return "", fmt.Errorf("access token has no supported developer login claim")
}

func validAdministratorName(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	return !strings.ContainsFunc(value, func(char rune) bool {
		return char == 0 || unicode.IsControl(char)
	})
}
