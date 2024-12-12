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

func Test_GetFunctionAppProperties(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		ran := false
		mockContext := mocks.NewMockContext(context.Background())
		azCli := newAzureClientFromMockContext(mockContext)

		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				strings.Contains(request.URL.Path, "/providers/Microsoft.Web/sites/FUNC_APP_NAME")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			ran = true

			response := armappservice.WebAppsClientGetResponse{
				Site: armappservice.Site{
					Location: to.Ptr("eastus2"),
					Kind:     to.Ptr("funcapp"),
					Name:     to.Ptr("FUNC_APP_NAME"),
					Properties: &armappservice.SiteProperties{
						DefaultHostName: to.Ptr("FUNC_APP_NAME.azurewebsites.net"),
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
		require.True(t, ran)
	})

	t.Run("Error", func(t *testing.T) {
		ran := false
		mockContext := mocks.NewMockContext(context.Background())
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

func Test_DeployFunctionAppUsingZipFile(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		ran := false
		mockContext := mocks.NewMockContext(context.Background())
		azCli := newAzureClientFromMockContext(mockContext)

		registerInfoMocks(mockContext, &ran)
		registerDeployMocks(mockContext, &ran)
		registerPollingMocks(mockContext, &ran)

		zipFile := bytes.NewReader([]byte{})

		res, err := azCli.DeployFunctionAppUsingZipFile(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			"RESOURCE_GROUP_ID",
			"FUNC_APP_NAME",
			zipFile,
			false,
		)

		require.NoError(t, err)
		require.True(t, ran)
		require.NotNil(t, res)
	})

	t.Run("Error", func(t *testing.T) {
		ran := false
		mockContext := mocks.NewMockContext(context.Background())
		azCli := newAzureClientFromMockContext(mockContext)

		registerInfoMocks(mockContext, &ran)
		registerConflictMocks(mockContext, &ran)

		zipFile := bytes.NewReader([]byte{})

		res, err := azCli.DeployFunctionAppUsingZipFile(
			*mockContext.Context,
			"SUBSCRIPTION_ID",
			"RESOURCE_GROUP_ID",
			"FUNC_APP_NAME",
			zipFile,
			false,
		)

		require.Nil(t, res)
		require.True(t, ran)
		require.Error(t, err)
	})
}

func registerInfoMocks(mockContext *mocks.MockContext, ran *bool) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		//nolint:lll
		return request.Method == http.MethodGet &&
			strings.Contains(
				request.URL.Path,
				"subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP_ID/providers/Microsoft.Web/sites/FUNC_APP_NAME",
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		*ran = true
		response, _ := mocks.CreateHttpResponseWithBody(
			request,
			http.StatusOK,
			armappservice.WebAppsClientGetResponse{
				Site: armappservice.Site{
					Properties: &armappservice.SiteProperties{
						HostNameSSLStates: []*armappservice.HostNameSSLState{
							{
								HostType: to.Ptr(armappservice.HostTypeStandard),
								Name:     to.Ptr("INVALID"),
							},
							{
								HostType: to.Ptr(armappservice.HostTypeRepository),
								Name:     to.Ptr("FUNC_APP_NAME_SCM_HOST"),
							},
						},
						//nolint:lll
						ServerFarmID: to.Ptr(
							"/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP_ID/providers/Microsoft.Web/serverfarms/FUNC_APP_PLAN_NAME",
						),
					},
				},
			},
		)

		return response, nil
	})

	mockContext.HttpClient.When(func(request *http.Request) bool {
		//nolint:lll
		return request.Method == http.MethodGet &&
			strings.Contains(
				request.URL.Path,
				"/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP_ID/providers/Microsoft.Web/serverfarms/FUNC_APP_PLAN_NAME",
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		response, _ := mocks.CreateHttpResponseWithBody(
			request,
			http.StatusOK,
			armappservice.PlansClientGetResponse{
				Plan: armappservice.Plan{
					SKU: &armappservice.SKUDescription{
						Name: to.Ptr("Y1"),
						Tier: to.Ptr("Dynamic"),
					},
				},
			})

		return response, nil
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
