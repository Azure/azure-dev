// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

// Test HasAppServiceDeployments
func Test_HasAppServiceDeployments(t *testing.T) {
	t.Run("HasDeployments", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		azCli := newAzureClientFromMockContext(mockContext)

		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				strings.Contains(request.URL.Path, "/deployments")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			response := armappservice.WebAppsClientListDeploymentsResponse{
				DeploymentCollection: armappservice.DeploymentCollection{
					Value: []*armappservice.Deployment{
						{
							ID:   to.Ptr("deployment-1"),
							Name: to.Ptr("deployment-1"),
						},
					},
				},
			}
			return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
		})

		hasDeployments, err := azCli.HasAppServiceDeployments(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			"RESOURCE_GROUP_ID",
			"WEB_APP_NAME",
		)

		require.NoError(t, err)
		require.True(t, hasDeployments)
	})

	t.Run("NoDeployments", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		azCli := newAzureClientFromMockContext(mockContext)

		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				strings.Contains(request.URL.Path, "/deployments")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			response := armappservice.WebAppsClientListDeploymentsResponse{
				DeploymentCollection: armappservice.DeploymentCollection{
					Value: []*armappservice.Deployment{},
				},
			}
			return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
		})

		hasDeployments, err := azCli.HasAppServiceDeployments(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			"RESOURCE_GROUP_ID",
			"WEB_APP_NAME",
		)

		require.NoError(t, err)
		require.False(t, hasDeployments)
	})
}

// Test GetAppServiceSlots
func Test_GetAppServiceSlots(t *testing.T) {
	t.Run("WithSlots", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		azCli := newAzureClientFromMockContext(mockContext)

		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				strings.Contains(request.URL.Path, "/slots")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			response := armappservice.WebAppsClientListSlotsResponse{
				WebAppCollection: armappservice.WebAppCollection{
					Value: []*armappservice.Site{
						{
							Name: to.Ptr("WEB_APP_NAME/staging"),
						},
						{
							Name: to.Ptr("WEB_APP_NAME/production"),
						},
					},
				},
			}
			return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
		})

		slots, err := azCli.GetAppServiceSlots(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			"RESOURCE_GROUP_ID",
			"WEB_APP_NAME",
		)

		require.NoError(t, err)
		require.Len(t, slots, 2)
		require.Equal(t, "staging", slots[0].Name)
		require.Equal(t, "production", slots[1].Name)
	})

	t.Run("NoSlots", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		azCli := newAzureClientFromMockContext(mockContext)

		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				strings.Contains(request.URL.Path, "/slots")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			response := armappservice.WebAppsClientListSlotsResponse{
				WebAppCollection: armappservice.WebAppCollection{
					Value: []*armappservice.Site{},
				},
			}
			return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
		})

		slots, err := azCli.GetAppServiceSlots(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			"RESOURCE_GROUP_ID",
			"WEB_APP_NAME",
		)

		require.NoError(t, err)
		require.Len(t, slots, 0)
	})
}

// Test DeployAppServiceSlotZip
func Test_DeployAppServiceSlotZip(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		azCli := newAzureClientFromMockContext(mockContext)

		// Mock GetSlot call
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				strings.Contains(request.URL.Path, "/slots/staging")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			response := armappservice.WebAppsClientGetSlotResponse{
				Site: armappservice.Site{
					Location: to.Ptr("eastus2"),
					Kind:     to.Ptr("app"),
					Name:     to.Ptr("WEB_APP_NAME/staging"),
					Properties: &armappservice.SiteProperties{
						DefaultHostName: to.Ptr("WEB_APP_NAME-staging.azurewebsites.net"),
						HostNameSSLStates: []*armappservice.HostNameSSLState{
							{
								HostType: to.Ptr(armappservice.HostTypeRepository),
								Name:     to.Ptr("WEB_APP_NAME_STAGING_SCM_HOST"),
							},
						},
					},
				},
			}
			return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
		})

		// Mock zip deploy call (returns accepted with location header)
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodPost &&
				request.URL.Host == "WEB_APP_NAME_STAGING_SCM_HOST" &&
				strings.Contains(request.URL.Path, "/api/zipdeploy")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			response, _ := mocks.CreateEmptyHttpResponse(request, http.StatusAccepted)
			response.Header.Set("Location", "https://WEB_APP_NAME_STAGING_SCM_HOST/deployments/latest")
			return response, nil
		})

		// Mock polling for deployment status
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				request.URL.Host == "WEB_APP_NAME_STAGING_SCM_HOST" &&
				strings.Contains(request.URL.Path, "/deployments/latest")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			completeStatus := azsdk.DeployStatusResponse{
				DeployStatus: azsdk.DeployStatus{
					Id:         "deployment-id",
					Status:     http.StatusOK,
					StatusText: "OK",
					Message:    "Deployment Complete",
					Complete:   true,
					Active:     true,
					SiteName:   "WEB_APP_NAME",
				},
			}
			return mocks.CreateHttpResponseWithBody(request, http.StatusOK, completeStatus)
		})

		zipFile := bytes.NewReader([]byte{})

		res, err := azCli.DeployAppServiceSlotZip(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			"RESOURCE_GROUP_ID",
			"WEB_APP_NAME",
			"staging",
			zipFile,
			func(s string) {},
		)

		require.NoError(t, err)
		require.NotNil(t, res)
	})

	t.Run("SlotNotFound", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		azCli := newAzureClientFromMockContext(mockContext)

		// Mock GetSlot call to return 404
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				strings.Contains(request.URL.Path, "/slots/nonexistent")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			response, _ := mocks.CreateEmptyHttpResponse(request, http.StatusNotFound)
			return response, nil
		})

		zipFile := bytes.NewReader([]byte{})

		res, err := azCli.DeployAppServiceSlotZip(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			"RESOURCE_GROUP_ID",
			"WEB_APP_NAME",
			"nonexistent",
			zipFile,
			func(s string) {},
		)

		require.Error(t, err)
		require.Nil(t, res)
	})
}
