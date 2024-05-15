// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azcli

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_GetStaticWebAppProperties(t *testing.T) {
	t.Run("NoErrors", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		azCli := newAzCliFromMockContext(mockContext)
		ran := false

		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				strings.Contains(request.URL.Path, "/providers/Microsoft.Web/staticSites/appName")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			ran = true

			response := armappservice.StaticSitesClientGetStaticSiteResponse{
				StaticSiteARMResource: armappservice.StaticSiteARMResource{
					Properties: &armappservice.StaticSite{
						DefaultHostname: convert.RefOf("https://test.com")},
				},
			}

			return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
		})

		props, err := azCli.GetStaticWebAppProperties(*mockContext.Context, "subID", "resourceGroupID", "appName")
		require.NoError(t, err)
		require.Equal(t, "https://test.com", props.DefaultHostname)
		require.True(t, ran)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		azCli := newAzCliFromMockContext(mockContext)
		ran := false

		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				strings.Contains(request.URL.Path, "/providers/Microsoft.Web/staticSites/appName")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			ran = true
			return mocks.CreateEmptyHttpResponse(request, http.StatusNotFound)
		})

		props, err := azCli.GetStaticWebAppProperties(*mockContext.Context, "subID", "resourceGroupID", "appName")
		require.Nil(t, props)
		require.True(t, ran)
		require.Error(t, err)
	})
}

func Test_GetStaticWebAppEnvironmentProperties(t *testing.T) {
	t.Run("NoErrors", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		azCli := newAzCliFromMockContext(mockContext)
		ran := false

		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				strings.Contains(request.URL.Path, "/providers/Microsoft.Web/staticSites/appName/builds/default")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			ran = true

			response := armappservice.StaticSitesClientGetStaticSiteBuildResponse{
				StaticSiteBuildARMResource: armappservice.StaticSiteBuildARMResource{
					Properties: &armappservice.StaticSiteBuildARMResourceProperties{
						Hostname: convert.RefOf("default-environment-name.azurestaticapps.net"),
						Status:   convert.RefOf(armappservice.BuildStatusReady),
					},
				},
			}

			return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
		})

		props, err := azCli.GetStaticWebAppEnvironmentProperties(
			*mockContext.Context,
			"subID",
			"resourceGroupID",
			"appName",
			"default",
		)
		require.NoError(t, err)
		require.Equal(t, "default-environment-name.azurestaticapps.net", props.Hostname)
		require.True(t, ran)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		azCli := newAzCliFromMockContext(mockContext)
		ran := false

		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				strings.Contains(request.URL.Path, "/providers/Microsoft.Web/staticSites/appName/builds/default")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			ran = true
			return mocks.CreateEmptyHttpResponse(request, http.StatusNotFound)
		})

		props, err := azCli.GetStaticWebAppEnvironmentProperties(
			*mockContext.Context,
			"subID",
			"resourceGroupID",
			"appName",
			"default",
		)
		require.Nil(t, props)
		require.True(t, ran)
		require.Error(t, err)
	})
}

func Test_GetStaticWebAppApiKey(t *testing.T) {
	t.Run("NoErrors", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		azCli := newAzCliFromMockContext(mockContext)
		ran := false

		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodPost &&
				strings.Contains(request.URL.Path, "/providers/Microsoft.Web/staticSites/appName/listSecrets")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			ran = true

			response := armappservice.StaticSitesClientListStaticSiteSecretsResponse{
				StringDictionary: armappservice.StringDictionary{
					Properties: map[string]*string{
						"apiKey": convert.RefOf("ABC123"),
					},
				},
			}

			return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
		})

		apiKey, err := azCli.GetStaticWebAppApiKey(*mockContext.Context, "subID", "resourceGroupID", "appName")
		require.NoError(t, err)
		require.Equal(t, "ABC123", *apiKey)
		require.True(t, ran)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		azCli := newAzCliFromMockContext(mockContext)
		ran := false

		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodPost &&
				strings.Contains(request.URL.Path, "/providers/Microsoft.Web/staticSites/appName/listSecrets")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			ran = true
			return mocks.CreateEmptyHttpResponse(request, http.StatusNotFound)
		})

		apiKey, err := azCli.GetStaticWebAppApiKey(*mockContext.Context, "subID", "resourceGroupID", "appName")
		require.Nil(t, apiKey)
		require.True(t, ran)
		require.Error(t, err)
	})
}
