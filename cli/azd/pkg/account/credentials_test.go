// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package account

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		subscriptionResolverFunc(func(ctx context.Context, subscriptionId string) (*Subscription, error) {
			if tenantId, has := subToTenant[subscriptionId]; has {
				return &Subscription{
					Id:                 subscriptionId,
					TenantId:           "resource-" + tenantId,
					UserAccessTenantId: tenantId,
				}, nil
			}
			return nil, errors.New("unknown subscription")
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
		cred1, err := provider.CredentialForSubscription(t.Context(), sub1)
		assert.NoError(t, err)
		assert.Equal(t, tenantToCred[tenant1], cred1)

		cred2, err := provider.CredentialForSubscription(t.Context(), sub2)
		assert.NoError(t, err)
		assert.Equal(t, tenantToCred[tenant2], cred2)
	})

	t.Run("Failure", func(t *testing.T) {
		_, err := provider.CredentialForSubscription(t.Context(), "11111111-1111-1111-1111-111111111111")
		assert.Error(t, err)
	})
}

func TestSubscriptionCredentialProvider_AADSTSErrors(t *testing.T) {
	t.Parallel()

	tenantId := "fafbff54-b655-4648-98a2-dc3ada4df86e"
	subscriptionId := "d0a01878-d7f8-41ce-a4bc-2ead16199965"

	t.Run("AADSTS70043_WithoutExistingSuggestion", func(t *testing.T) {
		provider := NewSubscriptionCredentialProvider(
			subscriptionResolverFunc(func(ctx context.Context, subId string) (*Subscription, error) {
				return &Subscription{
					Id:                 subId,
					TenantId:           "resource-" + tenantId,
					UserAccessTenantId: tenantId,
				}, nil
			}),
			multiTenantCredentialProviderFunc(func(ctx context.Context, tid string) (azcore.TokenCredential, error) {
				return nil, errors.New("AADSTS70043: The refresh token has expired")
			}),
		)

		_, err := provider.CredentialForSubscription(t.Context(), subscriptionId)
		assert.Error(t, err)

		// The error should be wrapped in an ErrorWithSuggestion
		var errWithSuggestion *internal.ErrorWithSuggestion
		assert.True(t, errors.As(err, &errWithSuggestion), "error should be wrapped in ErrorWithSuggestion")

		// Check that the suggestion includes tenant-specific guidance
		assert.Contains(t, errWithSuggestion.Suggestion, tenantId)
		assert.Contains(t, errWithSuggestion.Suggestion, "azd auth login --tenant-id")
		assert.Contains(t, errWithSuggestion.Message, tenantId)

		// The underlying error should contain AADSTS70043
		assert.Contains(t, errWithSuggestion.Error(), "AADSTS70043")
	})

	t.Run("AADSTS700082_RefreshTokenExpired", func(t *testing.T) {
		provider := NewSubscriptionCredentialProvider(
			subscriptionResolverFunc(func(ctx context.Context, subId string) (*Subscription, error) {
				return &Subscription{
					Id:                 subId,
					TenantId:           "resource-" + tenantId,
					UserAccessTenantId: tenantId,
				}, nil
			}),
			multiTenantCredentialProviderFunc(func(ctx context.Context, tid string) (azcore.TokenCredential, error) {
				return nil, errors.New("AADSTS700082: The refresh token has expired")
			}),
		)

		_, err := provider.CredentialForSubscription(t.Context(), subscriptionId)
		assert.Error(t, err)

		// The error should be wrapped in an ErrorWithSuggestion
		var errWithSuggestion *internal.ErrorWithSuggestion
		assert.True(t, errors.As(err, &errWithSuggestion), "error should be wrapped in ErrorWithSuggestion")

		// Check that the suggestion includes tenant-specific guidance
		assert.Contains(t, errWithSuggestion.Suggestion, tenantId)
		assert.Contains(t, errWithSuggestion.Suggestion, "azd auth login --tenant-id")
		assert.Contains(t, errWithSuggestion.Message, tenantId)

		// The underlying error should contain AADSTS700082
		assert.Contains(t, errWithSuggestion.Error(), "AADSTS700082")
	})

	t.Run("AADSTS700082_WithExistingSuggestion_PreservesWrappedFields", func(t *testing.T) {
		provider := NewSubscriptionCredentialProvider(
			subscriptionResolverFunc(func(ctx context.Context, subId string) (*Subscription, error) {
				return &Subscription{
					Id:                 subId,
					TenantId:           "resource-" + tenantId,
					UserAccessTenantId: tenantId,
				}, nil
			}),
			multiTenantCredentialProviderFunc(func(ctx context.Context, tid string) (azcore.TokenCredential, error) {
				return nil, &internal.ErrorWithSuggestion{
					Err:        errors.New("AADSTS700082: The refresh token has expired"),
					Message:    "Login expired for the current account.",
					Suggestion: "Run `azd auth login` to acquire a new token.",
				}
			}),
		)

		_, err := provider.CredentialForSubscription(t.Context(), subscriptionId)
		assert.Error(t, err)

		errWithSuggestion, ok := errors.AsType[*internal.ErrorWithSuggestion](err)
		assert.True(t, ok, "error should be wrapped in ErrorWithSuggestion")
		assert.Equal(t, "Login expired for the current account.", errWithSuggestion.Message)
		assert.Contains(t, errWithSuggestion.Suggestion, "Run `azd auth login` to acquire a new token.")
		assert.Contains(t, errWithSuggestion.Suggestion, tenantId)
	})

	t.Run("AADSTS700082_SuggestionAlreadyHasTenantID_NoRedundantAppend", func(t *testing.T) {
		provider := NewSubscriptionCredentialProvider(
			subscriptionResolverFunc(func(ctx context.Context, subId string) (*Subscription, error) {
				return &Subscription{
					Id:                 subId,
					TenantId:           "resource-" + tenantId,
					UserAccessTenantId: tenantId,
				}, nil
			}),
			multiTenantCredentialProviderFunc(func(ctx context.Context, tid string) (azcore.TokenCredential, error) {
				return nil, &internal.ErrorWithSuggestion{
					Err:     errors.New("AADSTS700082: The refresh token has expired"),
					Message: "Login expired for the current account.",
					Suggestion: fmt.Sprintf(
						"login expired, run `azd auth login --tenant-id %s` to acquire a new token.", tenantId),
				}
			}),
		)

		_, err := provider.CredentialForSubscription(t.Context(), subscriptionId)
		assert.Error(t, err)

		errWithSuggestion, ok := errors.AsType[*internal.ErrorWithSuggestion](err)
		require.True(t, ok)
		// Should NOT duplicate the --tenant-id guidance
		assert.Equal(t, 1, strings.Count(errWithSuggestion.Suggestion, "--tenant-id"))
	})

	t.Run("TenantLookupFailure_EnhancedError", func(t *testing.T) {
		provider := NewSubscriptionCredentialProvider(
			subscriptionResolverFunc(func(ctx context.Context, subId string) (*Subscription, error) {
				return nil, errors.New("failed to resolve tenant")
			}),
			multiTenantCredentialProviderFunc(func(ctx context.Context, tid string) (azcore.TokenCredential, error) {
				return &dummyCredential{}, nil
			}),
		)

		_, err := provider.CredentialForSubscription(t.Context(), subscriptionId)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "AZURE_TENANT_ID")
		assert.Contains(t, err.Error(), "manually set the subscription ID")
	})
}

// subscriptionResolverFunc implements [SubscriptionResolver] using a provided function.
type subscriptionResolverFunc func(ctx context.Context, subscriptionId string) (*Subscription, error)

func (r subscriptionResolverFunc) GetSubscription(
	ctx context.Context, subscriptionId string,
) (*Subscription, error) {
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
