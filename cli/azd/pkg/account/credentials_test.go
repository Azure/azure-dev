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
	"github.com/azure/azure-dev/cli/azd/internal"
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

func TestSubscriptionCredentialProvider_AADSTSErrors(t *testing.T) {
	t.Parallel()

	tenantId := "fafbff54-b655-4648-98a2-dc3ada4df86e"
	subscriptionId := "d0a01878-d7f8-41ce-a4bc-2ead16199965"

	t.Run("AADSTS70043_WithoutExistingSuggestion", func(t *testing.T) {
		provider := NewSubscriptionCredentialProvider(
			subscriptionTenantResolverFunc(func(ctx context.Context, subId string) (string, error) {
				return tenantId, nil
			}),
			multiTenantCredentialProviderFunc(func(ctx context.Context, tid string) (azcore.TokenCredential, error) {
				return nil, errors.New("AADSTS70043: The refresh token has expired")
			}),
		)

		_, err := provider.CredentialForSubscription(context.Background(), subscriptionId)
		assert.Error(t, err)
		
		// The error should be wrapped in an ErrorWithSuggestion
		var errWithSuggestion *internal.ErrorWithSuggestion
		assert.True(t, errors.As(err, &errWithSuggestion), "error should be wrapped in ErrorWithSuggestion")
		
		// Check that the suggestion includes tenant-specific guidance
		assert.Contains(t, errWithSuggestion.Suggestion, tenantId)
		assert.Contains(t, errWithSuggestion.Suggestion, "azd auth login --tenant-id")
		
		// The underlying error should contain AADSTS70043
		assert.Contains(t, errWithSuggestion.Error(), "AADSTS70043")
	})

	t.Run("AADSTS700082_RefreshTokenExpired", func(t *testing.T) {
		provider := NewSubscriptionCredentialProvider(
			subscriptionTenantResolverFunc(func(ctx context.Context, subId string) (string, error) {
				return tenantId, nil
			}),
			multiTenantCredentialProviderFunc(func(ctx context.Context, tid string) (azcore.TokenCredential, error) {
				return nil, errors.New("AADSTS700082: The refresh token has expired")
			}),
		)

		_, err := provider.CredentialForSubscription(context.Background(), subscriptionId)
		assert.Error(t, err)
		
		// The error should be wrapped in an ErrorWithSuggestion
		var errWithSuggestion *internal.ErrorWithSuggestion
		assert.True(t, errors.As(err, &errWithSuggestion), "error should be wrapped in ErrorWithSuggestion")
		
		// Check that the suggestion includes tenant-specific guidance
		assert.Contains(t, errWithSuggestion.Suggestion, tenantId)
		assert.Contains(t, errWithSuggestion.Suggestion, "azd auth login --tenant-id")
		
		// The underlying error should contain AADSTS700082
		assert.Contains(t, errWithSuggestion.Error(), "AADSTS700082")
	})

	t.Run("TenantLookupFailure_EnhancedError", func(t *testing.T) {
		provider := NewSubscriptionCredentialProvider(
			subscriptionTenantResolverFunc(func(ctx context.Context, subId string) (string, error) {
				return "", errors.New("failed to resolve tenant")
			}),
			multiTenantCredentialProviderFunc(func(ctx context.Context, tid string) (azcore.TokenCredential, error) {
				return &dummyCredential{}, nil
			}),
		)

		_, err := provider.CredentialForSubscription(context.Background(), subscriptionId)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "AZURE_TENANT_ID")
		assert.Contains(t, err.Error(), "manually set the subscription ID")
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
