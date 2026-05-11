// // Copyright (c) Microsoft Corporation. All rights reserved.
// // Licensed under the MIT License.

package azapi

import (
	"bytes"
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
		mockContext := mocks.NewMockContext(t.Context())
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
			false,
		)

		require.NoError(t, err)
		require.True(t, ran)
		require.NotNil(t, res)
	})

	t.Run("Error", func(t *testing.T) {
		ran := false
		mockContext := mocks.NewMockContext(t.Context())
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
			false,
		)

		require.Nil(t, res)
		require.True(t, ran)
		require.Error(t, err)
	})

	t.Run("InternalServerError", func(t *testing.T) {
		ran := false
		mockContext := mocks.NewMockContext(t.Context())
		azCli := newAzureClientFromMockContext(mockContext)

		registerIsLinuxWebAppMocks(mockContext, &ran)
		registerLinuxWebAppZipDeployMocks(mockContext, &ran)
		registerLinuxWebAppDeploy500SuccessfulMocks(mockContext, &ran)

		zipFile := bytes.NewReader([]byte{})

		res, err := azCli.DeployAppServiceZip(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			"RESOURCE_GROUP_ID",
			"LINUX_WEB_APP_NAME",
			zipFile,
			func(s string) {},
			false,
		)

		require.NoError(t, err)
		require.True(t, ran)
		require.NotNil(t, res)
	})

	t.Run("Logic App Success", func(t *testing.T) {
		ran := false
		mockContext := mocks.NewMockContext(t.Context())
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
			false,
		)

		require.NoError(t, err)
		require.True(t, ran)
		require.NotNil(t, res)
	})

	t.Run("SkipStatusCheck", func(t *testing.T) {
		ran := false
		mockContext := mocks.NewMockContext(t.Context())
		azCli := newAzureClientFromMockContext(mockContext)

		registerIsLinuxWebAppMocks(mockContext, &ran)
		// Register basic zip deploy mocks (not status tracking mocks)
		// When skipStatusCheck=true, it falls through to the basic Deploy() path
		registerLinuxWebAppBasicZipDeployMocks(mockContext, &ran)

		zipFile := bytes.NewReader([]byte{})

		res, err := azCli.DeployAppServiceZip(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			"RESOURCE_GROUP_ID",
			"LINUX_WEB_APP_NAME",
			zipFile,
			func(s string) {},
			true,
		)

		require.NoError(t, err)
		require.True(t, ran)
		require.NotNil(t, res)
	})

	t.Run("StoppedApp", func(t *testing.T) {
		ran := false
		mockContext := mocks.NewMockContext(t.Context())
		azCli := newAzureClientFromMockContext(mockContext)

		// Use stopped app mock instead of running app mock
		registerIsStoppedLinuxWebAppMocks(mockContext, &ran)
		// When the app is stopped, DeployTrackStatus is skipped and basic Deploy() is used
		registerLinuxWebAppBasicZipDeployMocks(mockContext, &ran)

		zipFile := bytes.NewReader([]byte{})

		res, err := azCli.DeployAppServiceZip(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			"RESOURCE_GROUP_ID",
			"LINUX_WEB_APP_NAME",
			zipFile,
			func(s string) {},
			false,
		)

		require.NoError(t, err)
		require.True(t, ran)
		require.NotNil(t, res)
	})
}

func Test_isAppStopped(t *testing.T) {
	t.Run("Stopped", func(t *testing.T) {
		app := &armappservice.WebAppsClientGetResponse{
			Site: armappservice.Site{
				Properties: &armappservice.SiteProperties{
					State: new("Stopped"),
				},
			},
		}
		require.True(t, isAppStopped(app))
	})

	t.Run("Running", func(t *testing.T) {
		app := &armappservice.WebAppsClientGetResponse{
			Site: armappservice.Site{
				Properties: &armappservice.SiteProperties{
					State: new("Running"),
				},
			},
		}
		require.False(t, isAppStopped(app))
	})

	t.Run("NilState", func(t *testing.T) {
		app := &armappservice.WebAppsClientGetResponse{
			Site: armappservice.Site{
				Properties: &armappservice.SiteProperties{
					State: nil,
				},
			},
		}
		require.False(t, isAppStopped(app))
	})

	t.Run("NilProperties", func(t *testing.T) {
		app := &armappservice.WebAppsClientGetResponse{
			Site: armappservice.Site{
				Properties: nil,
			},
		}
		require.False(t, isAppStopped(app))
	})
}

func registerLinuxWebAppBasicZipDeployMocks(mockContext *mocks.MockContext, ran *bool) {
	// Zip deploy request that returns a Location header (basic deploy path)
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

	// Polling response for basic deploy
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			request.URL.Host == "LINUX_WEB_APP_NAME_SCM_HOST" &&
			strings.Contains(request.URL.Path, "/deployments/latest")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*ran = true
		completeStatus := azsdk.DeployStatusResponse{
			DeployStatus: azsdk.DeployStatus{
				Id:         "ID",
				Status:     http.StatusOK,
				StatusText: "OK",
				Message:    "Deployment Complete",
				Complete:   true,
				Active:     true,
				SiteName:   "LINUX_WEB_APP_NAME",
			},
		}
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, completeStatus)
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
				Location: new("eastus2"),
				Kind:     new("app,linux"),
				Name:     new("LINUX_WEB_APP_NAME"),
				Properties: &armappservice.SiteProperties{
					DefaultHostName: new("LINUX_WEB_APP_NAME.azurewebsites.net"),
					SiteConfig: &armappservice.SiteConfig{
						LinuxFxVersion: new("Python"),
					},
					HostNameSSLStates: []*armappservice.HostNameSSLState{
						{
							HostType: to.Ptr(armappservice.HostTypeRepository),
							Name:     new("LINUX_WEB_APP_NAME_SCM_HOST"),
						},
					},
				},
			},
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})
}

func registerIsStoppedLinuxWebAppMocks(mockContext *mocks.MockContext, ran *bool) {
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
				Location: new("eastus2"),
				Kind:     new("app,linux"),
				Name:     new("LINUX_WEB_APP_NAME"),
				Properties: &armappservice.SiteProperties{
					DefaultHostName: new("LINUX_WEB_APP_NAME.azurewebsites.net"),
					State:           new("Stopped"),
					SiteConfig: &armappservice.SiteConfig{
						LinuxFxVersion: new("Python"),
					},
					HostNameSSLStates: []*armappservice.HostNameSSLState{
						{
							HostType: to.Ptr(armappservice.HostTypeRepository),
							Name:     new("LINUX_WEB_APP_NAME_SCM_HOST"),
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
				Location: new("eastus2"),
				Kind:     new("functionapp"),
				Name:     new("WINDOWS_LOGIC_APP_NAME"),
				Properties: &armappservice.SiteProperties{
					DefaultHostName: new("WINDOWS_LOGIC_APP_NAME.azurewebsites.net"),
					SiteConfig: &armappservice.SiteConfig{
						LinuxFxVersion: new(""),
					},
					HostNameSSLStates: []*armappservice.HostNameSSLState{
						{
							HostType: to.Ptr(armappservice.HostTypeRepository),
							Name:     new("WINDOWS_LOGIC_APP_SCM_HOST"),
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
						NumberOfInstancesSuccessful: new(int32(1)),
						NumberOfInstancesFailed:     new(int32(0)),
						NumberOfInstancesInProgress: new(int32(0)),
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
						NumberOfInstancesSuccessful: new(int32(0)),
						NumberOfInstancesFailed:     new(int32(1)),
						NumberOfInstancesInProgress: new(int32(0)),
					},
				},
			},
		)

		return response, nil
	})
}

func registerLinuxWebAppDeploy500SuccessfulMocks(mockContext *mocks.MockContext, ran *bool) {
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
			int(http.StatusInternalServerError),
			map[string]any{
				"code": "InternalServerError",
				"details": []any{
					map[string]any{"message": nil},
					map[string]any{"code": "InternalServerError"},
					map[string]any{
						"errorEntity": map[string]any{
							"code":    "InternalServerError",
							"message": nil,
						},
					},
				},
			},
		)
		return response, nil
	})

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

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && request.URL.Host == "LINUX_WEB_APP_NAME_SCM_HOST" &&
			strings.Contains(request.URL.Path, "/deployments/latest")
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
