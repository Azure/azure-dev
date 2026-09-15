// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcloud "github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func TestNewAccountService(t *testing.T) {
	t.Parallel()
	svc := NewAccountService(nil, nil, nil)
	require.NotNil(t, svc)
}

func TestAccountService_LookupTenant_EmptySubscriptionId(t *testing.T) {
	t.Parallel()
	svc := NewAccountService(nil, nil, nil)
	_, err := svc.LookupTenant(t.Context(), &azdext.LookupTenantRequest{
		SubscriptionId: "",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
	require.Contains(t, st.Message(), "subscription id is required")
}

type mockAccountSubscriptions struct {
	*account.SubscriptionsManager
	mock.Mock
}

func (m *mockAccountSubscriptions) GetSubscription(
	ctx context.Context, subscriptionID string,
) (*account.Subscription, error) {
	args := m.Called(ctx, subscriptionID)
	subscription, _ := args.Get(0).(*account.Subscription)
	return subscription, args.Error(1)
}

type mockAccountPrincipalType struct {
	mock.Mock
}

func (m *mockAccountPrincipalType) CurrentPrincipalType(ctx context.Context) (auth.PrincipalType, error) {
	args := m.Called(ctx)
	return args.Get(0).(auth.PrincipalType), args.Error(1)
}

func TestAccountService_GetCurrentPrincipal(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name          string
		accessTenant  string
		principalType auth.PrincipalType
		protoType     azdext.PrincipalType
	}{
		{"user", "resource-tenant", auth.UserPrincipalType, azdext.PrincipalType_PRINCIPAL_TYPE_USER},
		{"guest", "home-tenant", auth.UserPrincipalType, azdext.PrincipalType_PRINCIPAL_TYPE_USER},
		{
			"service principal", "resource-tenant", auth.ServicePrincipalType,
			azdext.PrincipalType_PRINCIPAL_TYPE_SERVICE_PRINCIPAL,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			subscriptions := &mockAccountSubscriptions{}
			subscriptions.On("GetSubscription", mock.Anything, "sub-123").Return(&account.Subscription{
				Id: "sub-123", TenantId: "resource-tenant", UserAccessTenantId: tt.accessTenant,
			}, nil).Twice()
			principalTypes := &mockAccountPrincipalType{}
			principalTypes.On("CurrentPrincipalType", mock.Anything).Return(tt.principalType, nil).Twice()

			mockContext := mocks.NewMockContext(ctx)
			azureCloud := cloud.AzurePublic()
			armScope := azureCloud.Configuration.Services[azcloud.ResourceManager].Audience + "/.default"
			var tokenCalls atomic.Int32
			userProfile := azapi.NewUserProfileService(
				&mocks.MockMultiTenantCredentialProvider{TokenMap: map[string]mocks.MockCredentials{
					"resource-tenant": {
						GetTokenFn: func(
							ctx context.Context, options policy.TokenRequestOptions,
						) (azcore.AccessToken, error) {
							tokenCalls.Add(1)
							assert.Equal(t, []string{armScope}, options.Scopes)
							// No principal-type claims: the host must use login details, not token heuristics.
							return azcore.AccessToken{
								Token:     mocks.CreateJwtToken(t, map[string]string{"oid": "resource-object-id"}),
								ExpiresOn: time.Now().Add(time.Hour),
							}, nil
						},
					},
					"home-tenant": {
						GetTokenFn: func(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
							t.Error("principal lookup must not acquire a home-tenant token")
							return azcore.AccessToken{}, errors.New("unexpected home tenant")
						},
					},
				}},
				&azcore.ClientOptions{Transport: mockContext.HttpClient},
				azureCloud,
			)
			svc := &accountService{
				subscriptionsManager: subscriptions, userProfileService: userProfile, principalTypeProvider: principalTypes,
			}

			server := newServerWithContainerService(azdext.UnimplementedContainerServiceServer{})
			server.accountService = svc
			info, err := server.Start()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, server.Stop()) })
			token, err := GenerateExtensionToken(&extensions.Extension{Id: "azd.internal.test", Namespace: "test"}, info)
			require.NoError(t, err)
			ctx = azdext.WithAccessToken(ctx, token)
			client, err := azdext.NewAzdClient(azdext.WithAddress(info.Address))
			require.NoError(t, err)
			t.Cleanup(client.Close)

			response, err := client.Account().GetCurrentPrincipal(ctx, &azdext.GetCurrentPrincipalRequest{
				SubscriptionId: "sub-123",
			})
			require.NoError(t, err)
			require.Equal(t, "resource-object-id", response.ObjectId)
			require.Equal(t, tt.protoType, response.PrincipalType)

			connection, err := grpc.NewClient(info.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, connection.Close()) })
			betaResponse, err := v1beta.NewAccountServiceClient(connection).GetCurrentPrincipal(
				ctx, &v1beta.GetCurrentPrincipalRequest{SubscriptionId: "sub-123"},
			)
			require.NoError(t, err)
			require.Equal(t, response.ObjectId, betaResponse.ObjectId)
			require.Equal(t, int32(response.PrincipalType), int32(betaResponse.PrincipalType))
			require.EqualValues(t, 2, tokenCalls.Load())
			subscriptions.AssertExpectations(t)
			principalTypes.AssertExpectations(t)
		})
	}
}

func TestAccountService_GetCurrentPrincipal_InvalidRequest(t *testing.T) {
	t.Parallel()
	for _, request := range []*azdext.GetCurrentPrincipalRequest{
		nil, {}, {SubscriptionId: " \t"},
	} {
		// No dependencies: validation must run before authentication or subscription lookup.
		response, err := NewAccountService(nil, nil, nil).GetCurrentPrincipal(t.Context(), request)
		require.Nil(t, response)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	}
}

func TestAccountService_GetCurrentPrincipal_LookupErrors(t *testing.T) {
	t.Parallel()
	subscriptionErr := errors.New("subscription unavailable")
	for _, tt := range []struct {
		name          string
		principalType auth.PrincipalType
		loginErr      error
		subErr        error
		wantErr       error
		wantCode      codes.Code
	}{
		{name: "not logged in", loginErr: auth.ErrNoCurrentUser, wantErr: auth.ErrNoCurrentUser},
		{name: "login cancelled", loginErr: context.Canceled, wantErr: context.Canceled},
		{name: "unsupported type", principalType: "unknown", wantCode: codes.Internal},
		{name: "subscription", principalType: auth.UserPrincipalType, subErr: subscriptionErr, wantErr: subscriptionErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			subscriptions := &mockAccountSubscriptions{}
			if tt.subErr != nil {
				subscriptions.On("GetSubscription", t.Context(), "sub-123").Return(nil, tt.subErr).Once()
			}
			principalTypes := &mockAccountPrincipalType{}
			principalTypes.On("CurrentPrincipalType", t.Context()).Return(tt.principalType, tt.loginErr).Once()
			svc := &accountService{principalTypeProvider: principalTypes, subscriptionsManager: subscriptions}
			response, err := svc.GetCurrentPrincipal(t.Context(), &azdext.GetCurrentPrincipalRequest{
				SubscriptionId: "sub-123",
			})
			require.Nil(t, response)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.Equal(t, tt.wantCode, status.Code(err))
			}
			subscriptions.AssertExpectations(t)
			principalTypes.AssertExpectations(t)
		})
	}
}

func TestAccountService_GetCurrentPrincipal_GraphFallback(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name       string
		graphID    string
		statusCode int
		wantError  string
	}{
		{name: "success", graphID: "graph-object-id", statusCode: http.StatusOK},
		{name: "lookup failure", statusCode: http.StatusForbidden, wantError: "fetching current principal information"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			subscriptions := &mockAccountSubscriptions{}
			subscriptions.On("GetSubscription", t.Context(), "sub-123").Return(&account.Subscription{
				Id: "sub-123", TenantId: "resource-tenant", UserAccessTenantId: "home-tenant",
			}, nil).Once()
			principalTypes := &mockAccountPrincipalType{}
			principalTypes.On("CurrentPrincipalType", t.Context()).Return(auth.UserPrincipalType, nil).Once()
			mockContext := mocks.NewMockContext(t.Context())
			mockContext.HttpClient.When(func(request *http.Request) bool {
				return request.Method == http.MethodGet && request.URL.Host == "graph.microsoft.com"
			}).RespondFn(func(request *http.Request) (*http.Response, error) {
				return mocks.CreateHttpResponseWithBody(request, tt.statusCode, &graphsdk.UserProfile{Id: tt.graphID})
			})
			userProfile := azapi.NewUserProfileService(
				&mocks.MockMultiTenantCredentialProvider{TokenMap: map[string]mocks.MockCredentials{
					"resource-tenant": {
						GetTokenFn: func(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
							return azcore.AccessToken{
								Token: mocks.CreateJwtToken(t, map[string]string{}), ExpiresOn: time.Now().Add(time.Hour),
							}, nil
						},
					},
				}},
				&azcore.ClientOptions{Transport: mockContext.HttpClient},
				cloud.AzurePublic(),
			)
			svc := &accountService{
				subscriptionsManager: subscriptions, principalTypeProvider: principalTypes, userProfileService: userProfile,
			}
			response, err := svc.GetCurrentPrincipal(t.Context(), &azdext.GetCurrentPrincipalRequest{
				SubscriptionId: "sub-123",
			})
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				require.Nil(t, response)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.graphID, response.ObjectId)
				require.Equal(t, azdext.PrincipalType_PRINCIPAL_TYPE_USER, response.PrincipalType)
			}
			subscriptions.AssertExpectations(t)
			principalTypes.AssertExpectations(t)
		})
	}
}
