// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package account

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/assert"
)

func TestSubscriptionCredentialProvider(t *testing.T) {
	t.Parallel()

	tenant1 := "fafbff54-b655-4648-98a2-dc3ada4df86e"
	tenant2 := "e615e058-6ff1-46e6-ab20-a1d5efd0f68c"

	sub1 := "d0a01878-d7f8-41ce-a4bc-2ead16199965"
	sub2 := "bbc7e1fa-a1aa-47d8-b05b-91f6ebe569fe"

	tenantToCred := map[string]azcore.TokenCredential{
		tenant1: &dummyCredential{},
		tenant2: &dummyCredential{},
	}

	subToTenant := map[string]string{
		sub1: tenant1,
		sub2: tenant2,
	}

	provider := NewSubscriptionCredentialProvider(
		subscriptionTenantResolverFunc(func(ctx context.Context, subscriptionId string) (string, error) {
			if tenantId, has := subToTenant[subscriptionId]; has {
				return tenantId, nil
			} else {
				return "", errors.New("unknown subscription")
			}
		}),
		multiTenantCredentialProviderFunc(func(ctx context.Context, tenantId string) (azcore.TokenCredential, error) {
			if credential, has := tenantToCred[tenantId]; has {
				return credential, nil
			} else {
				return nil, errors.New("unknown tenant")
			}
		}),
	)

	t.Run("Success", func(t *testing.T) {
		cred1, err := provider.CredentialForSubscription(context.Background(), sub1)
		assert.NoError(t, err)
		assert.Equal(t, tenantToCred[tenant1], cred1)

		cred2, err := provider.CredentialForSubscription(context.Background(), sub2)
		assert.NoError(t, err)
		assert.Equal(t, tenantToCred[tenant2], cred2)
	})

	t.Run("Failure", func(t *testing.T) {
		_, err := provider.CredentialForSubscription(context.Background(), "11111111-1111-1111-1111-111111111111")
		assert.Error(t, err)
	})
}

// subscriptionTenantResolverFunc implements [SubscriptionTenantResolver] using a provided function.
type subscriptionTenantResolverFunc func(ctx context.Context, subscriptionId string) (string, error)

func (r subscriptionTenantResolverFunc) LookupTenant(ctx context.Context, subscriptionId string) (string, error) {
	return r(ctx, subscriptionId)
}

// multiTenantCredentialProviderFunc implements [auth.MultiTenantCredentialProviderF] using a provided function.
type multiTenantCredentialProviderFunc func(ctx context.Context, tenantId string) (azcore.TokenCredential, error)

func (p multiTenantCredentialProviderFunc) GetTokenCredential(
	ctx context.Context,
	tenantId string,
) (azcore.TokenCredential, error) {
	return p(ctx, tenantId)
}

// dummyCredential implements [azcore.TokenCredential] and returns a fixed token.
type dummyCredential struct{}

func (c *dummyCredential) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{
		Token:     "a-fake-token",
		ExpiresOn: time.Now(),
	}, nil
}
