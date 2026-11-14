// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func credentialForSubscription(
	ctx context.Context,
	client *azdext.AzdClient,
	subscriptionID string,
) (azcore.TokenCredential, error) {
	tenant, err := client.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
		SubscriptionId: subscriptionID,
	})
	if err != nil {
		return nil, fmt.Errorf("looking up the user access tenant for the selected subscription: %w", err)
	}
	if tenant.GetTenantId() == "" {
		return nil, errorsNoTenant()
	}
	credential, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{
			TenantID:                   tenant.GetTenantId(),
			AdditionallyAllowedTenants: []string{"*"},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("creating the azd Azure credential: %w", err)
	}
	return credential, nil
}
