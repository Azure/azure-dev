// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

type fakeSubscriptionResolver struct {
	subscription *account.Subscription
	getCalls     int
}

func (f *fakeSubscriptionResolver) GetSubscription(
	ctx context.Context, subscriptionId string,
) (*account.Subscription, error) {
	f.getCalls++
	return f.subscription, nil
}

func TestPrincipalIDProvider_CurrentPrincipalIdUsesSubscriptionTenant(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	userProfileService := azapi.NewUserProfileService(
		&mocks.MockMultiTenantCredentialProvider{
			TokenMap: map[string]mocks.MockCredentials{
				"resource-tenant": {
					GetTokenFn: func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
						return azcore.AccessToken{
							Token: mocks.CreateJwtToken(t, map[string]string{
								"oid": "this-is-a-test",
							}),
							ExpiresOn: time.Now().Add(time.Hour),
						}, nil
					},
				},
			},
		},
		&azcore.ClientOptions{
			Transport: mockContext.HttpClient,
		},
		cloud.AzurePublic(),
	)

	resolver := &fakeSubscriptionResolver{
		subscription: &account.Subscription{
			Id:                 "sub-123",
			TenantId:           "resource-tenant",
			UserAccessTenantId: "home-tenant",
		},
	}

	provider := NewPrincipalIdProvider(
		environment.NewWithValues("test", map[string]string{
			environment.SubscriptionIdEnvVarName: "sub-123",
		}),
		userProfileService,
		resolver,
		nil,
	)

	principalId, err := provider.CurrentPrincipalId(t.Context())
	require.NoError(t, err)
	require.Equal(t, "this-is-a-test", principalId)
	require.Equal(t, 1, resolver.getCalls)
}
