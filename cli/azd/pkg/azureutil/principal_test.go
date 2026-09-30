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

	mockContext := mocks.NewMockContext(t.Context())
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

	for _, tt := range []struct {
		name    string
		graphID string
	}{
		{name: "success", graphID: "graph-user-id"},
		{name: "empty object id"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mockContext := mocks.NewMockContext(t.Context())
			mockContext.HttpClient.When(func(request *http.Request) bool {
				return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/me")
			}).RespondFn(func(request *http.Request) (*http.Response, error) {
				return mocks.CreateHttpResponseWithBody(request, http.StatusOK, &graphsdk.UserProfile{
					Id: tt.graphID,
				})
			})

			userProfile := azapi.NewUserProfileService(
				&mocks.MockMultiTenantCredentialProvider{
					TokenMap: map[string]mocks.MockCredentials{
						"resource-tenant": {
							GetTokenFn: func(
								ctx context.Context, options policy.TokenRequestOptions,
							) (azcore.AccessToken, error) {
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
			if tt.graphID == "" {
				require.ErrorContains(t, err, "signed-in user response did not contain an object id")
				require.ErrorContains(t, err, "getting oid from token: no oid claim")
				require.Empty(t, principalId)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.graphID, principalId)
			}
		})
	}
}

func TestGetCurrentPrincipalId_ReturnsJoinedErrorWhenTokenAndGraphFail(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())
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
