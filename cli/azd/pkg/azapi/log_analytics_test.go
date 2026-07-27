// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/operationalinsights/armoperationalinsights/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/test/mocks"
)

func Test_AzureClient_GetLogAnalyticsWorkspace(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	client := newAzureClientFromMockContext(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			strings.Contains(req.URL.Path, "/workspaces/my-workspace")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armoperationalinsights.Workspace{
				ID: new(
					"/subscriptions/SUB/resourceGroups/RG" +
						"/providers/Microsoft.OperationalInsights" +
						"/workspaces/my-workspace"),
				Name:     new("my-workspace"),
				Location: new("eastus"),
			})
	})

	result, err := client.GetLogAnalyticsWorkspace(*mockCtx.Context, "SUB", "RG", "my-workspace")
	require.NoError(t, err)
	assert.Equal(t, "my-workspace", result.Name)
	assert.Contains(t, result.Id, "my-workspace")
}

func Test_AzureClient_PurgeLogAnalyticsWorkspace(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	client := newAzureClientFromMockContext(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodDelete &&
			strings.Contains(req.URL.Path, "/workspaces/my-workspace")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(req, http.StatusOK)
	})

	err := client.PurgeLogAnalyticsWorkspace(*mockCtx.Context, "SUB", "RG", "my-workspace")
	require.NoError(t, err)
}
