// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azcli

import (
	"context"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
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
	_, _ = azCli.GetResource(ctx, "SUBSCRIPTION_ID", "RESOURCE_ID", "API_VERSION")

	userAgent, ok := rawResponse.Request.Header["User-Agent"]
	if !ok {
		require.Fail(t, "missing User-Agent header")
	}

	require.Contains(t, userAgent[0], "azsdk-go")
	require.Contains(t, userAgent[0], "azdev")
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
	_, _ = azCli.GetResource(ctx, "SUBSCRIPTION_ID", "RESOURCE_ID", "API_VERSION")

	userAgent, ok := rawResponse.Request.Header["User-Agent"]
	if !ok {
		require.Fail(t, "missing User-Agent header")
	}

	require.Contains(t, userAgent[0], "azsdk-go")
	require.Contains(t, userAgent[0], "azdev")
}

func newAzCliFromMockContext(mockContext *mocks.MockContext) AzCli {
	return NewAzCli(
		mockaccount.SubscriptionCredentialProviderFunc(func(_ context.Context, _ string) (azcore.TokenCredential, error) {
			return mockContext.Credentials, nil
		}),
		mockContext.HttpClient,
		NewAzCliArgs{},
		mockContext.ArmClientOptions,
	)
}
