// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appconfiguration/armappconfiguration"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/test/mocks"
)

func Test_AzureClient_GetAppConfig(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	client := newAzureClientFromMockContext(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			strings.Contains(req.URL.Path, "/configurationStores/my-config")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armappconfiguration.ConfigurationStore{
				ID: new(
					"/subscriptions/SUB/resourceGroups/RG" +
						"/providers/Microsoft.AppConfiguration" +
						"/configurationStores/my-config"),
				Name:     new("my-config"),
				Location: new("westus"),
				Properties: &armappconfiguration.ConfigurationStoreProperties{
					EnablePurgeProtection: new(true),
				},
			})
	})

	result, err := client.GetAppConfig(*mockCtx.Context, "SUB", "RG", "my-config")
	require.NoError(t, err)
	assert.Equal(t, "my-config", result.Name)
	assert.Equal(t, "westus", result.Location)
}

func Test_AzureClient_PurgeAppConfig(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	client := newAzureClientFromMockContext(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodPost &&
			strings.Contains(req.URL.Path, "/purge")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(req, http.StatusOK)
	})

	err := client.PurgeAppConfig(*mockCtx.Context, "SUB", "my-config", "westus")
	require.NoError(t, err)
}
