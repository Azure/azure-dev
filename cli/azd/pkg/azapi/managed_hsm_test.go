// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/test/mocks"
)

func Test_AzureClient_GetManagedHSM(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	client := newAzureClientFromMockContext(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			strings.Contains(req.URL.Path, "/managedHSMs/my-hsm")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armkeyvault.ManagedHsm{
				ID:       new("/subscriptions/SUB/resourceGroups/RG/providers/Microsoft.KeyVault/managedHSMs/my-hsm"),
				Name:     new("my-hsm"),
				Location: new("eastus"),
				Properties: &armkeyvault.ManagedHsmProperties{
					EnableSoftDelete:      new(true),
					EnablePurgeProtection: new(false),
				},
			})
	})

	result, err := client.GetManagedHSM(*mockCtx.Context, "SUB", "RG", "my-hsm")
	require.NoError(t, err)
	assert.Equal(t, "my-hsm", result.Name)
	assert.Equal(t, "eastus", result.Location)
}

func Test_AzureClient_PurgeManagedHSM(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	client := newAzureClientFromMockContext(mockCtx)

	pollURL := "https://management.azure.com/subscriptions/SUB/" +
		"providers/Microsoft.KeyVault/locations/eastus/" +
		"operationResults/op123?api-version=2023-07-01"

	// Initial POST returns 202 with async operation header
	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodPost &&
			strings.Contains(req.URL.Path, "/purge")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		resp, _ := mocks.CreateEmptyHttpResponse(req, http.StatusAccepted)
		resp.Header.Set("Azure-AsyncOperation", pollURL)
		return resp, nil
	})

	// Poll endpoint returns 200 with completed status
	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			strings.Contains(req.URL.Path, "/operationResults/")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK, map[string]any{
			"status": "Succeeded",
		})
	})

	err := client.PurgeManagedHSM(*mockCtx.Context, "SUB", "my-hsm", "eastus")
	require.NoError(t, err)
}
