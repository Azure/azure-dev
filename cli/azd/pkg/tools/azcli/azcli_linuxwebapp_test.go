// // Copyright (c) Microsoft Corporation. All rights reserved.
// // Licensed under the MIT License.

package azcli

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

// Test deployment status api (linux web app only)
func Test_DeployLinuxWebAppUsingZipFile(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		ran := false
		mockContext := mocks.NewMockContext(context.Background())
		azCli := newAzCliFromMockContext(mockContext)

		registerIsLinuxWebAppMocks(mockContext, &ran)
		// registerLinuxWebAppDeployRuntimeSuccessMocks(mockContext, &ran)
		// registerLinuxWebAppPollingMocks(mockContext, &ran)

		zipFile := bytes.NewBuffer([]byte{})

		res, err := azCli.DeployAppServiceZip(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			"RESOURCE_GROUP_ID",
			"LINUX_WEB_APP_NAME",
			zipFile,
			func(string) {},
		)

		require.NoError(t, err)
		require.True(t, ran)
		require.NotNil(t, res)
	})

	t.Run("Error", func(t *testing.T) {
		ran := false
		mockContext := mocks.NewMockContext(context.Background())
		azCli := newAzCliFromMockContext(mockContext)

		registerLinuxWebAppRuntimeSuccessfulInfoMocks(mockContext, &ran)

		zipFile := bytes.NewBuffer([]byte{})

		res, err := azCli.DeployAppServiceZip(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			"RESOURCE_GROUP_ID",
			"LINUX_WEB_APP_NAME",
			zipFile,
			func(string) {},
		)

		require.Nil(t, res)
		require.True(t, ran)
		require.Error(t, err)
	})
}

func registerIsLinuxWebAppMocks(mockContext *mocks.MockContext, ran *bool) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(request.URL.Path,
				"/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP_ID/providers/Microsoft.Web/sites/LINUX_WEB_APP_NAME")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*ran = true
		response := armappservice.WebAppsClientGetResponse{
			Site: armappservice.Site{
				Location: convert.RefOf("eastus2"),
				Kind:     convert.RefOf("appserivce"),
				Name:     convert.RefOf("LINUX_WEB_APP_NAME"),
				Properties: &armappservice.SiteProperties{
					DefaultHostName: convert.RefOf("LINUX_WEB_APP_NAME.azurewebsites.net"),
					SiteConfig: &armappservice.SiteConfig{
						LinuxFxVersion: convert.RefOf("Python"),
					},
				},
			},
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})
}

func registerLinuxWebAppDeployRuntimeSuccessfulMocks(mockContext *mocks.MockContext, ran *bool) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		//nolint:lll
		return request.Method == http.MethodGet &&
			strings.Contains(
				request.URL.Path,
				"/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP_ID/providers/Microsoft.Web/sites/LINUX_WEB_APP_NAME",
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*ran = true
		response, _ := mocks.CreateHttpResponseWithBody(
			request,
			http.StatusOK,
			armappservice.WebAppsClientGetProductionSiteDeploymentStatusResponse{
				CsmDeploymentStatus: armappservice.CsmDeploymentStatus{
					Properties: &armappservice.CsmDeploymentStatusProperties{
						Status: convert.RefOf(armappservice.DeploymentBuildStatusRuntimeSuccessful),
					},
				},
			},
		)

		return response, nil
	})
}

func registerLinuxWebAppDeployMocks(mockContext *mocks.MockContext, ran *bool) {
	// Original call to start the deployment operation
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost &&
			request.URL.Host == "LINUX_WEB_APP_NAME_SCM_HOST" &&
			strings.Contains(request.URL.Path, "/api/zipdeploy")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*ran = true
		response, _ := mocks.CreateEmptyHttpResponse(request, http.StatusAccepted)
		response.Header.Set("Location", "https://LINUX_WEB_APP_NAME_SCM_HOST/deployments/latest")

		return response, nil
	})
}
func registerLinuxWebAppPollingMocks(mockContext *mocks.MockContext, ran *bool) {
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
				SiteName:   "LINUX_WEB_APP_NAME",
				LogUrl:     "https://log.url",
			},
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, completeStatus)
	})
}
