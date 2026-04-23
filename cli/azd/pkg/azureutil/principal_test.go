// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azureutil

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestGetCurrentPrincipalId_PrefersOidFromAccessToken(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	userProfile := azapi.NewUserProfileService(
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

	principalId, err := GetCurrentPrincipalId(*mockContext.Context, userProfile, "resource-tenant")
	require.NoError(t, err)
	require.Equal(t, "this-is-a-test", principalId)
}

func TestGetCurrentPrincipalId_FallsBackToGraphWhenOidMissing(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/me")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, &graphsdk.UserProfile{
			Id: "graph-user-id",
		})
	})

	userProfile := azapi.NewUserProfileService(
		&mocks.MockMultiTenantCredentialProvider{
			TokenMap: map[string]mocks.MockCredentials{
				"resource-tenant": {
					GetTokenFn: func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
						return azcore.AccessToken{
							Token: mocks.CreateJwtToken(t, map[string]string{
								"test": "fail",
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

	principalId, err := GetCurrentPrincipalId(*mockContext.Context, userProfile, "resource-tenant")
	require.NoError(t, err)
	require.Equal(t, "graph-user-id", principalId)
}

func TestGetCurrentPrincipalId_ReturnsJoinedErrorWhenTokenAndGraphFail(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/me")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(request, http.StatusBadRequest)
	})

	userProfile := azapi.NewUserProfileService(
		&mocks.MockMultiTenantCredentialProvider{
			TokenMap: map[string]mocks.MockCredentials{
				"resource-tenant": {
					GetTokenFn: func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
						return azcore.AccessToken{
							Token: mocks.CreateJwtToken(t, map[string]string{
								"test": "fail",
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

	principalId, err := GetCurrentPrincipalId(*mockContext.Context, userProfile, "resource-tenant")
	require.Error(t, err)
	require.Empty(t, principalId)
	require.ErrorContains(t, err, "resolving current principal ID from token oid and Graph fallback")
	require.ErrorContains(t, err, "getting oid from token: no oid claim")
	require.ErrorContains(t, err, "getting signed-in user id: failed retrieving current user profile")
}
