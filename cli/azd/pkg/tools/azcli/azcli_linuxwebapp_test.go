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
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

// Test deployment status api (linux web app only)
func Test_DeployTrackLinuxWebAppStatus(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		ran := false
		mockContext := mocks.NewMockContext(context.Background())
		azCli := newAzCliFromMockContext(mockContext)

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
		azCli := newAzCliFromMockContext(mockContext)

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
					HostNameSSLStates: []*armappservice.HostNameSSLState{
						{
							HostType: convert.RefOf(armappservice.HostTypeRepository),
							Name:     convert.RefOf("LINUX_WEB_APP_NAME_SCM_HOST"),
						},
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
						Status:                      convert.RefOf(armappservice.DeploymentBuildStatusRuntimeSuccessful),
						NumberOfInstancesSuccessful: convert.RefOf(int32(1)),
						NumberOfInstancesFailed:     convert.RefOf(int32(0)),
						NumberOfInstancesInProgress: convert.RefOf(int32(0)),
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
						Status:                      convert.RefOf(armappservice.DeploymentBuildStatusRuntimeFailed),
						NumberOfInstancesSuccessful: convert.RefOf(int32(0)),
						NumberOfInstancesFailed:     convert.RefOf(int32(1)),
						NumberOfInstancesInProgress: convert.RefOf(int32(0)),
					},
				},
			},
		)

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
