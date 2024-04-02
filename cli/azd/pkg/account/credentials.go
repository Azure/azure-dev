// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package account

import (
	"context"
	"log"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
)

// SubscriptionCredentialProvider provides an [azcore.TokenCredential] configured
// to use the tenant id that corresponds to the tenant the given subscription
// is located in.
type SubscriptionCredentialProvider interface {
	CredentialForSubscription(ctx context.Context, subscriptionId string) (azcore.TokenCredential, error)
}

func (p *account) CredentialForSubscription(
	ctx context.Context,
	subscriptionId string,
) (azcore.TokenCredential, error) {
	tenantId, err := p.LookupTenant(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	return p.CredentialForTenant(ctx, tenantId)
}

// Gets an authenticated token credential for the given tenant. If tenantId is empty, uses the default home tenant.
func (t *account) CredentialForTenant(ctx context.Context, tenantId string) (azcore.TokenCredential, error) {
	if val, ok := t.tenantCredentials.Load(tenantId); ok {
		return val.(azcore.TokenCredential), nil
	}

	log.Printf("Getting credential for tenant %s", tenantId)

	credential, err := t.auth.CredentialForCurrentUser(ctx, &auth.CredentialForCurrentUserOptions{
		TenantID: tenantId,
	})

	if err != nil {
		return nil, err
	}

	if _, err := auth.EnsureLoggedInCredential(ctx, credential, t.cloud); err != nil {
		return nil, err
	}

	t.tenantCredentials.Store(tenantId, credential)
	return credential, nil
}
