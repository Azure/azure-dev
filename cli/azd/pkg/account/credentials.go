// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package account

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
)

// SubscriptionCredentialProvider provides an [azcore.TokenCredential] configured
// to use the tenant id that corresponds to the tenant the given subscription
// is located in.
type SubscriptionCredentialProvider interface {
	CredentialForSubscription(ctx context.Context, subscriptionId string) (azcore.TokenCredential, error)
}

type subscriptionCredentialProvider struct {
	credProvider auth.MultiTenantCredentialProvider
	subResolver  SubscriptionTenantResolver
}

func NewSubscriptionCredentialProvider(
	subResolver SubscriptionTenantResolver,
	credProvider auth.MultiTenantCredentialProvider,
) SubscriptionCredentialProvider {
	return &subscriptionCredentialProvider{
		credProvider: credProvider,
		subResolver:  subResolver,
	}
}

func (p *subscriptionCredentialProvider) CredentialForSubscription(
	ctx context.Context,
	subscriptionId string,
) (azcore.TokenCredential, error) {
	tenantId, err := p.subResolver.LookupTenant(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	return p.credProvider.GetTokenCredential(ctx, tenantId)
}
