// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azcli

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAZCLIWithUserAgent(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && request.URL.Path == "/RESOURCE_ID"
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		response := armresources.ClientGetByIDResponse{
			GenericResource: armresources.GenericResource{
				ID:       convert.RefOf("RESOURCE_ID"),
				Kind:     convert.RefOf("RESOURCE_KIND"),
				Name:     convert.RefOf("RESOURCE_NAME"),
				Type:     convert.RefOf("RESOURCE_TYPE"),
				Location: convert.RefOf("RESOURCE_LOCATION"),
			},
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})

	var rawResponse *http.Response
	ctx := runtime.WithCaptureResponse(*mockContext.Context, &rawResponse)

	azCli := newAzCliFromMockContext(mockContext)
	// We don't care about the actual response or if an error occurred
	// Any API call that leverages the Go SDK is fine
	_, _ = azCli.GetResource(ctx, "SUBSCRIPTION_ID", "RESOURCE_ID")

	userAgent, ok := rawResponse.Request.Header["User-Agent"]
	if !ok {
		require.Fail(t, "missing User-Agent header")
	}

	require.Contains(t, userAgent[0], "azsdk-go")
	require.Contains(t, userAgent[0], "azdev")
}

func TestAZCliGetAccessTokenTranslatesErrors(t *testing.T) {
	//nolint:lll
	tests := []struct {
		name   string
		stderr string
		expect error
	}{
		{
			name:   "AADSTS70043",
			stderr: "AADSTS70043: The refresh token has expired or is invalid due to sign-in frequency checks by conditional access. The token was issued on {issueDate} and the maximum allowed lifetime for this request is {time}.",
			expect: ErrAzCliRefreshTokenExpired,
		},
		{
			name:   "AADSTS700082",
			stderr: "AADSTS700082: The refresh token has expired due to inactivity. The token was issued on {issueDate} and was inactive for {time}.",
			expect: ErrAzCliRefreshTokenExpired,
		},
		{
			name:   "GetAccessTokenDoubleQuotes",
			stderr: `Please run "az login" to setup account.`,
			expect: ErrAzCliNotLoggedIn,
		},
		{
			name:   "GetAccessTokenSingleQuotes",
			stderr: `Please run 'az login' to setup account.`,
			expect: ErrAzCliNotLoggedIn,
		},
		{
			name:   "GetAccessTokenDoubleQuotesAccessAccount",
			stderr: `Please run "az login" to access your accounts.`,
			expect: ErrAzCliNotLoggedIn,
		},
		{
			name:   "GetAccessTokenSingleQuotesAccessAccount",
			stderr: `Please run 'az login' to access your accounts.`,
			expect: ErrAzCliNotLoggedIn,
		},
		{
			name:   "GetAccessTokenErrorNoSubscriptionFound",
			stderr: `ERROR: No subscription found`,
			expect: ErrAzCliNotLoggedIn,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(context.Background())
			mockCredential := mocks.MockCredentials{
				GetTokenFn: func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
					return azcore.AccessToken{}, errors.New(test.stderr)
				},
			}

			azCli := NewAzCli(&mockCredential, NewAzCliArgs{
				EnableDebug:     true,
				EnableTelemetry: true,
			})

			_, err := azCli.GetAccessToken(*mockContext.Context)
			assert.True(t, errors.Is(err, test.expect))
		})
	}
}

func Test_AzSdk_User_Agent_Policy(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && request.URL.Path == "/RESOURCE_ID"
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		response := armresources.ClientGetByIDResponse{
			GenericResource: armresources.GenericResource{
				ID:       convert.RefOf("RESOURCE_ID"),
				Kind:     convert.RefOf("RESOURCE_KIND"),
				Name:     convert.RefOf("RESOURCE_NAME"),
				Type:     convert.RefOf("RESOURCE_TYPE"),
				Location: convert.RefOf("RESOURCE_LOCATION"),
			},
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})

	var rawResponse *http.Response
	ctx := runtime.WithCaptureResponse(*mockContext.Context, &rawResponse)

	azCli := newAzCliFromMockContext(mockContext)
	// We don't care about the actual response or if an error occurred
	// Any API call that leverages the Go SDK is fine
	_, _ = azCli.GetResource(ctx, "SUBSCRIPTION_ID", "RESOURCE_ID")

	userAgent, ok := rawResponse.Request.Header["User-Agent"]
	if !ok {
		require.Fail(t, "missing User-Agent header")
	}

	require.Contains(t, userAgent[0], "azsdk-go")
	require.Contains(t, userAgent[0], "azdev")
}

func newAzCliFromMockContext(mockContext *mocks.MockContext) AzCli {
	return NewAzCli(mockContext.Credentials, NewAzCliArgs{
		HttpClient: mockContext.HttpClient,
	})
}
