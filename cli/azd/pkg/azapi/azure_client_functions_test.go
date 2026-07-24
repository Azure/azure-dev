// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

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

func Test_GetFunctionAppProperties(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		ran := false
		mockContext := mocks.NewMockContext(t.Context())
		azCli := newAzureClientFromMockContext(mockContext)

		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				strings.Contains(request.URL.Path, "/providers/Microsoft.Web/sites/FUNC_APP_NAME")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			ran = true

			response := armappservice.WebAppsClientGetResponse{
				Site: armappservice.Site{
					Location: new("eastus2"),
					Kind:     new("functionapp,linux,container"),
					Name:     new("FUNC_APP_NAME"),
					Properties: &armappservice.SiteProperties{
						DefaultHostName: new("FUNC_APP_NAME.azurewebsites.net"),
						//nolint:lll
						ServerFarmID: new(
							"/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP_ID/providers/Microsoft.Web/serverfarms/FUNC_APP_PLAN",
						),
						HostNameSSLStates: []*armappservice.HostNameSSLState{
							{
								HostType: to.Ptr(armappservice.HostTypeRepository),
								Name:     new("FUNC_APP_NAME.scm.azurewebsites.net"),
							},
						},
						SiteConfig: &armappservice.SiteConfig{
							LinuxFxVersion: new("DOCKER|registry.azurecr.io/function:latest"),
						},
					},
				},
			}

			return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
		})

		props, err := azCli.GetFunctionAppProperties(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			"RESOURCE_GROUP_ID",
			"FUNC_APP_NAME",
		)
		require.NoError(t, err)
		require.Equal(t, []string{"FUNC_APP_NAME.azurewebsites.net"}, props.HostNames)
		require.Equal(t,
			"/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP_ID/"+
				"providers/Microsoft.Web/serverfarms/FUNC_APP_PLAN",
			props.ServerFarmID,
		)
		require.Len(t, props.HostNameSslStates, 1)
		require.NotNil(t, props.ContainerConfiguration)
		require.True(t, props.ContainerConfiguration.IsLinux)
		require.True(t, props.ContainerConfiguration.IsContainer)
		require.True(t, ran)
	})

	t.Run("Error", func(t *testing.T) {
		ran := false
		mockContext := mocks.NewMockContext(t.Context())
		azCli := newAzureClientFromMockContext(mockContext)

		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				strings.Contains(request.URL.Path, "/providers/Microsoft.Web/sites/FUNC_APP_NAME")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			ran = true

			return mocks.CreateEmptyHttpResponse(request, http.StatusNotFound)
		})

		props, err := azCli.GetFunctionAppProperties(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			"RESOURCE_GROUP_ID",
			"FUNC_APP_NAME",
		)

		require.Nil(t, props)
		require.True(t, ran)
		require.Error(t, err)
	})
}

func Test_GetFunctionAppPlan(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		azCli := newAzureClientFromMockContext(mockContext)

		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				strings.Contains(request.URL.Path, "/serverfarms/FUNC_APP_PLAN")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			return mocks.CreateHttpResponseWithBody(request, http.StatusOK, armappservice.Plan{
				Name: new("FUNC_APP_PLAN"),
				SKU: &armappservice.SKUDescription{
					Name: new("FC1"),
					Tier: new("FlexConsumption"),
				},
			})
		})

		plan, err := azCli.GetFunctionAppPlan(*mockContext.Context, &AzCliFunctionAppProperties{
			ServerFarmID: "/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP_ID/" +
				"providers/Microsoft.Web/serverfarms/FUNC_APP_PLAN",
		})

		require.NoError(t, err)
		require.Equal(t, "FUNC_APP_PLAN", *plan.Name)
		require.Equal(t, "FlexConsumption", *plan.SKU.Tier)
	})

	t.Run("InvalidPlanResourceId", func(t *testing.T) {
		azCli := newAzureClientFromMockContext(mocks.NewMockContext(t.Context()))

		plan, err := azCli.GetFunctionAppPlan(t.Context(), &AzCliFunctionAppProperties{
			ServerFarmID: "not-a-resource-id",
		})

		require.Nil(t, plan)
		require.Error(t, err)
	})

	t.Run("ApiError", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		azCli := newAzureClientFromMockContext(mockContext)

		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				strings.Contains(request.URL.Path, "/serverfarms/FUNC_APP_PLAN")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			return mocks.CreateEmptyHttpResponse(request, http.StatusNotFound)
		})

		plan, err := azCli.GetFunctionAppPlan(*mockContext.Context, &AzCliFunctionAppProperties{
			ServerFarmID: "/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP_ID/" +
				"providers/Microsoft.Web/serverfarms/FUNC_APP_PLAN",
		})

		require.Nil(t, plan)
		require.Error(t, err)
	})
}

func Test_DeployFunctionAppUsingZipFileFlexConsumption(t *testing.T) {
	props := &AzCliFunctionAppProperties{
		HostNameSslStates: []*armappservice.HostNameSSLState{
			{
				HostType: to.Ptr(armappservice.HostTypeRepository),
				Name:     new("FUNC_APP_NAME_SCM_HOST"),
			},
		},
	}

	t.Run("Success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		azCli := newAzureClientFromMockContext(mockContext)

		var remoteBuild string
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodPost &&
				request.URL.Host == "FUNC_APP_NAME_SCM_HOST" &&
				request.URL.Path == "/api/publish"
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			remoteBuild = request.URL.Query().Get("RemoteBuild")
			return mocks.CreateHttpResponseWithBody(request, http.StatusAccepted, "deployment-id")
		})
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				request.URL.Host == "FUNC_APP_NAME_SCM_HOST" &&
				request.URL.Path == "/api/deployments/deployment-id"
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			return mocks.CreateHttpResponseWithBody(request, http.StatusOK, azsdk.PublishResponse{
				Id:         "deployment-id",
				Status:     azsdk.PublishStatusSuccess,
				StatusText: "Deployment successful",
			})
		})

		status, err := azCli.DeployFunctionAppUsingZipFileFlexConsumption(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			props,
			"FUNC_APP_NAME",
			bytes.NewReader([]byte("zip")),
			true,
		)

		require.NoError(t, err)
		require.Equal(t, "Deployment successful", *status)
		require.Equal(t, "true", remoteBuild)
	})

	t.Run("PublishError", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		azCli := newAzureClientFromMockContext(mockContext)

		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodPost &&
				request.URL.Host == "FUNC_APP_NAME_SCM_HOST" &&
				request.URL.Path == "/api/publish"
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			return mocks.CreateEmptyHttpResponse(request, http.StatusBadRequest)
		})

		status, err := azCli.DeployFunctionAppUsingZipFileFlexConsumption(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			props,
			"FUNC_APP_NAME",
			bytes.NewReader(nil),
			false,
		)

		require.Nil(t, status)
		require.ErrorContains(t, err, "publishing zip file")
	})
}

func Test_DeployFunctionAppUsingZipFileRegular(t *testing.T) {
	props := &AzCliFunctionAppProperties{
		HostNames: []string{"FUNC_APP_NAME.azurewebsites.net"},
		HostNameSslStates: []*armappservice.HostNameSSLState{
			{
				HostType: to.Ptr(armappservice.HostTypeStandard),
				Name:     new("INVALID"),
			},
			{
				HostType: to.Ptr(armappservice.HostTypeRepository),
				Name:     new("FUNC_APP_NAME_SCM_HOST"),
			},
		},
	}

	t.Run("Success", func(t *testing.T) {
		ran := false
		mockContext := mocks.NewMockContext(t.Context())
		azCli := newAzureClientFromMockContext(mockContext)

		registerDeployMocks(mockContext, &ran)
		registerPollingMocks(mockContext, &ran)

		zipFile := bytes.NewReader([]byte{})

		res, err := azCli.DeployFunctionAppUsingZipFileRegular(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			props,
			"FUNC_APP_NAME",
			zipFile,
		)

		require.NoError(t, err)
		require.True(t, ran)
		require.NotNil(t, res)
	})

	t.Run("Error", func(t *testing.T) {
		ran := false
		mockContext := mocks.NewMockContext(t.Context())
		azCli := newAzureClientFromMockContext(mockContext)

		registerConflictMocks(mockContext, &ran)

		zipFile := bytes.NewReader([]byte{})

		res, err := azCli.DeployFunctionAppUsingZipFileRegular(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			props,
			"FUNC_APP_NAME",
			zipFile,
		)

		require.Nil(t, res)
		require.True(t, ran)
		require.Error(t, err)
	})
}

func registerConflictMocks(mockContext *mocks.MockContext, ran *bool) {
	// Original call to start the deployment operation
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost &&
			request.URL.Host == "FUNC_APP_NAME_SCM_HOST" &&
			strings.Contains(request.URL.Path, "/api/zipdeploy")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*ran = true
		return mocks.CreateEmptyHttpResponse(request, http.StatusConflict)
	})
}

func registerDeployMocks(mockContext *mocks.MockContext, ran *bool) {
	// Original call to start the deployment operation
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost &&
			request.URL.Host == "FUNC_APP_NAME_SCM_HOST" &&
			strings.Contains(request.URL.Path, "/api/zipdeploy")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*ran = true
		response, _ := mocks.CreateEmptyHttpResponse(request, http.StatusAccepted)
		response.Header.Set("Location", "https://FUNC_APP_NAME_SCM_HOST/deployments/latest")

		return response, nil
	})
}
func registerPollingMocks(mockContext *mocks.MockContext, ran *bool) {
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
				SiteName:   "FUNC_APP_NAME",
				LogUrl:     "https://log.url",
			},
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, completeStatus)
	})
}
