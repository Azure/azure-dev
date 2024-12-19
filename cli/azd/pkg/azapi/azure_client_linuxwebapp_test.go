// // Copyright (c) Microsoft Corporation. All rights reserved.
// // Licensed under the MIT License.

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

// Test deployment status api (linux web app only)
func Test_DeployTrackLinuxWebAppStatus(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		ran := false
		mockContext := mocks.NewMockContext(context.Background())
		azCli := newAzureClientFromMockContext(mockContext)

		registerIsLinuxWebAppMocks(mockContext, &ran)
		registerLinuxWebAppZipDeployMocks(mockContext, &ran)
		registerLinuxWebAppDeployRuntimeSuccessfulMocks(mockContext, &ran)

		zipFile := bytes.NewReader([]byte{})

		res, err := azCli.DeployAppServiceZip(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			"RESOURCE_GROUP_ID",
			"LINUX_WEB_APP_NAME",
			zipFile,
			func(s string) {},
		)

		require.NoError(t, err)
		require.True(t, ran)
		require.NotNil(t, res)
	})

	t.Run("Error", func(t *testing.T) {
		ran := false
		mockContext := mocks.NewMockContext(context.Background())
		azCli := newAzureClientFromMockContext(mockContext)

		registerIsLinuxWebAppMocks(mockContext, &ran)
		registerLinuxWebAppZipDeployMocks(mockContext, &ran)
		registerLinuxWebAppDeployRuntimeFailedMocks(mockContext, &ran)

		zipFile := bytes.NewReader([]byte{})

		res, err := azCli.DeployAppServiceZip(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			"RESOURCE_GROUP_ID",
			"LINUX_WEB_APP_NAME",
			zipFile,
			func(s string) {},
		)

		require.Nil(t, res)
		require.True(t, ran)
		require.Error(t, err)
	})

	t.Run("Error", func(t *testing.T) {
		ran := false
		mockContext := mocks.NewMockContext(context.Background())
		azCli := newAzureClientFromMockContext(mockContext)

		registerLogicAppMocks(mockContext, &ran)
		registerLogicAppZipDeployMocks(mockContext, &ran)
		registerLogicAppPollingMocks(mockContext, &ran)

		zipFile := bytes.NewReader([]byte{})

		res, err := azCli.DeployAppServiceZip(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			"RESOURCE_GROUP_ID",
			"WINDOWS_LOGIC_APP_NAME",
			zipFile,
			func(s string) {},
		)

		require.NoError(t, err)
		require.True(t, ran)
		require.NotNil(t, res)
	})
}

func registerIsLinuxWebAppMocks(mockContext *mocks.MockContext, ran *bool) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(
				request.URL.Path,
				//nolint:lll
				"/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP_ID/providers/Microsoft.Web/sites/LINUX_WEB_APP_NAME",
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*ran = true
		response := armappservice.WebAppsClientGetResponse{
			Site: armappservice.Site{
				Location: to.Ptr("eastus2"),
				Kind:     to.Ptr("app,linux"),
				Name:     to.Ptr("LINUX_WEB_APP_NAME"),
				Properties: &armappservice.SiteProperties{
					DefaultHostName: to.Ptr("LINUX_WEB_APP_NAME.azurewebsites.net"),
					SiteConfig: &armappservice.SiteConfig{
						LinuxFxVersion: to.Ptr("Python"),
					},
					HostNameSSLStates: []*armappservice.HostNameSSLState{
						{
							HostType: to.Ptr(armappservice.HostTypeRepository),
							Name:     to.Ptr("LINUX_WEB_APP_NAME_SCM_HOST"),
						},
					},
				},
			},
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})
}

func registerLogicAppMocks(mockContext *mocks.MockContext, ran *bool) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(
				request.URL.Path,
				//nolint:lll
				"/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP_ID/providers/Microsoft.Web/sites/WINDOWS_LOGIC_APP_NAME",
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*ran = true
		response := armappservice.WebAppsClientGetResponse{
			Site: armappservice.Site{
				Location: to.Ptr("eastus2"),
				Kind:     to.Ptr("functionapp"),
				Name:     to.Ptr("WINDOWS_LOGIC_APP_NAME"),
				Properties: &armappservice.SiteProperties{
					DefaultHostName: to.Ptr("WINDOWS_LOGIC_APP_NAME.azurewebsites.net"),
					SiteConfig: &armappservice.SiteConfig{
						LinuxFxVersion: to.Ptr(""),
					},
					HostNameSSLStates: []*armappservice.HostNameSSLState{
						{
							HostType: to.Ptr(armappservice.HostTypeRepository),
							Name:     to.Ptr("WINDOWS_LOGIC_APP_SCM_HOST"),
						},
					},
				},
			},
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})
}

func registerLogicAppPollingMocks(mockContext *mocks.MockContext, ran *bool) {
	// Polling call to check on the deployment status
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/deployments/latest")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*ran = true
		completeStatus := azsdk.DeployStatusResponse{
			DeployStatus: azsdk.DeployStatus{
				Id:         "ID",
				Status:     http.StatusOK,
				StatusText: "OK",
				Message:    "Deployment Complete",
				Progress:   nil,
				Complete:   true,
				Active:     true,
				SiteName:   "WINDOWS_LOGIC_APP_NAME",
				LogUrl:     "https://log.url",
			},
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, completeStatus)
	})
}

func registerLinuxWebAppDeployRuntimeSuccessfulMocks(mockContext *mocks.MockContext, ran *bool) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		//nolint:lll
		return request.Method == http.MethodGet &&
			strings.Contains(
				request.URL.Path,
				"/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP_ID/providers/Microsoft.Web/sites/LINUX_WEB_APP_NAME/deploymentStatus/00000000-0000-0000-0000-000000000000",
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*ran = true
		response, _ := mocks.CreateHttpResponseWithBody(
			request,
			http.StatusOK,
			armappservice.WebAppsClientGetProductionSiteDeploymentStatusResponse{
				CsmDeploymentStatus: armappservice.CsmDeploymentStatus{
					Properties: &armappservice.CsmDeploymentStatusProperties{
						Status:                      to.Ptr(armappservice.DeploymentBuildStatusRuntimeSuccessful),
						NumberOfInstancesSuccessful: to.Ptr(int32(1)),
						NumberOfInstancesFailed:     to.Ptr(int32(0)),
						NumberOfInstancesInProgress: to.Ptr(int32(0)),
					},
				},
			},
		)

		return response, nil
	})
}

func registerLinuxWebAppDeployRuntimeFailedMocks(mockContext *mocks.MockContext, ran *bool) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		//nolint:lll
		return request.Method == http.MethodGet &&
			strings.Contains(
				request.URL.Path,
				"/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP_ID/providers/Microsoft.Web/sites/LINUX_WEB_APP_NAME/deploymentStatus/00000000-0000-0000-0000-000000000000",
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*ran = true
		response, _ := mocks.CreateHttpResponseWithBody(
			request,
			http.StatusOK,
			armappservice.WebAppsClientGetProductionSiteDeploymentStatusResponse{
				CsmDeploymentStatus: armappservice.CsmDeploymentStatus{
					Properties: &armappservice.CsmDeploymentStatusProperties{
						Status:                      to.Ptr(armappservice.DeploymentBuildStatusRuntimeFailed),
						NumberOfInstancesSuccessful: to.Ptr(int32(0)),
						NumberOfInstancesFailed:     to.Ptr(int32(1)),
						NumberOfInstancesInProgress: to.Ptr(int32(0)),
					},
				},
			},
		)

		return response, nil
	})
}

func registerLogicAppZipDeployMocks(mockContext *mocks.MockContext, ran *bool) {
	// Original call to start the deployment operation
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost &&
			request.URL.Host == "WINDOWS_LOGIC_APP_SCM_HOST" &&
			strings.Contains(request.URL.Path, "/api/zipdeploy")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*ran = true
		response, _ := mocks.CreateEmptyHttpResponse(request, http.StatusAccepted)
		response.Header.Set("Location", "https://WINDOWS_LOGIC_APP_SCM_HOST/deployments/latest")

		return response, nil
	})
}

func registerLinuxWebAppZipDeployMocks(mockContext *mocks.MockContext, ran *bool) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost &&
			request.URL.Host == "LINUX_WEB_APP_NAME_SCM_HOST" &&
			strings.Contains(request.URL.Path, "/api/zipdeploy")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*ran = true
		response, _ := mocks.CreateEmptyHttpResponse(request, http.StatusAccepted)
		response.Header.Set("Scm-Deployment-Id", "00000000-0000-0000-0000-000000000000")

		return response, nil
	})
}
