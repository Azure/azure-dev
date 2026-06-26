// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/apimanagement/armapimanagement"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/test/mocks"
)

func Test_AzureClient_GetApim(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	client := newAzureClientFromMockContext(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			strings.Contains(req.URL.Path, "/Microsoft.ApiManagement/service/my-apim")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armapimanagement.ServiceResource{
				ID:       new("/subscriptions/SUB/resourceGroups/RG/providers/Microsoft.ApiManagement/service/my-apim"),
				Name:     new("my-apim"),
				Location: new("eastus"),
			})
	})

	result, err := client.GetApim(*mockCtx.Context, "SUB", "RG", "my-apim")
	require.NoError(t, err)
	assert.Equal(t, "my-apim", result.Name)
	assert.Equal(t, "eastus", result.Location)
}

func Test_AzureClient_PurgeApim(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	client := newAzureClientFromMockContext(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodDelete &&
			strings.Contains(req.URL.Path, "/deletedservices/")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(req, http.StatusOK)
	})

	err := client.PurgeApim(*mockCtx.Context, "SUB", "my-apim", "eastus")
	require.NoError(t, err)
}
