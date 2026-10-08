// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

type fakeSubscriptionResolver struct {
	subscription   *account.Subscription
	getCalls       int
	subscriptionId string
	err            error
}

func (f *fakeSubscriptionResolver) GetSubscription(
	ctx context.Context, subscriptionId string,
) (*account.Subscription, error) {
	f.getCalls++
	f.subscriptionId = subscriptionId
	return f.subscription, f.err
}

func TestPrincipalIDProvider_CurrentPrincipalIdUsesSubscriptionTenant(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())
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
	require.Equal(t, "sub-123", resolver.subscriptionId)
}

type scopedSubscriptionEnvironment struct {
	*environment.Environment
	subscriptionId string
}

func (e *scopedSubscriptionEnvironment) GetSubscriptionId() string {
	return e.subscriptionId
}

func TestPrincipalIDProvider_UsesScopedSubscription(t *testing.T) {
	t.Parallel()

	env := &scopedSubscriptionEnvironment{
		Environment: environment.NewWithValues("shared", map[string]string{
			environment.SubscriptionIdEnvVarName: "shared-subscription",
		}),
		subscriptionId: "layer-subscription",
	}
	resolverErr := errors.New("subscription lookup failed")
	resolver := &fakeSubscriptionResolver{err: resolverErr}
	root := ioc.NewNestedContainer(nil)
	ioc.RegisterInstance(root, env.BackingEnv())
	ioc.RegisterInstance[*azapi.UserProfileService](root, nil)
	ioc.RegisterInstance[account.SubscriptionResolver](root, resolver)
	ioc.RegisterInstance[*auth.Manager](root, nil)
	root.MustRegisterScoped(func(backing *environment.Environment) environment.ScopedEnvironment { return backing })
	root.MustRegisterScoped(NewPrincipalIdProvider)
	scope, err := root.NewScope()
	require.NoError(t, err)
	ioc.RegisterInstance[environment.ScopedEnvironment](scope, env)
	var provider CurrentPrincipalIdProvider
	require.NoError(t, scope.Resolve(&provider))
	_, err = provider.CurrentPrincipalId(t.Context())
	require.ErrorIs(t, err, resolverErr)
	require.Equal(t, "layer-subscription", resolver.subscriptionId)
	require.Equal(t, "shared-subscription", env.BackingEnv().GetSubscriptionId())
}
