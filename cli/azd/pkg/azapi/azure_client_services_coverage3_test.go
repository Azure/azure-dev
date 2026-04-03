// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/apimanagement/armapimanagement"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appconfiguration/armappconfiguration"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/operationalinsights/armoperationalinsights/v2"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- APIM ---

func Test_AzureClient_GetApim_Coverage3(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	client := newAzureClientFromMockContext(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			strings.Contains(req.URL.Path, "/Microsoft.ApiManagement/service/my-apim")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armapimanagement.ServiceResource{
				ID:       to.Ptr("/subscriptions/SUB/resourceGroups/RG/providers/Microsoft.ApiManagement/service/my-apim"),
				Name:     to.Ptr("my-apim"),
				Location: to.Ptr("eastus"),
			})
	})

	result, err := client.GetApim(*mockCtx.Context, "SUB", "RG", "my-apim")
	require.NoError(t, err)
	assert.Equal(t, "my-apim", result.Name)
	assert.Equal(t, "eastus", result.Location)
}

func Test_AzureClient_PurgeApim_Coverage3(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	client := newAzureClientFromMockContext(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodDelete &&
			strings.Contains(req.URL.Path, "/deletedservices/")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(req, http.StatusOK)
	})

	err := client.PurgeApim(*mockCtx.Context, "SUB", "my-apim", "eastus")
	require.NoError(t, err)
}

// --- AppConfig ---

func Test_AzureClient_GetAppConfig_Coverage3(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	client := newAzureClientFromMockContext(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			strings.Contains(req.URL.Path, "/configurationStores/my-config")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armappconfiguration.ConfigurationStore{
				ID:       to.Ptr("/subscriptions/SUB/resourceGroups/RG/providers/Microsoft.AppConfiguration/configurationStores/my-config"),
				Name:     to.Ptr("my-config"),
				Location: to.Ptr("westus"),
				Properties: &armappconfiguration.ConfigurationStoreProperties{
					EnablePurgeProtection: to.Ptr(true),
				},
			})
	})

	result, err := client.GetAppConfig(*mockCtx.Context, "SUB", "RG", "my-config")
	require.NoError(t, err)
	assert.Equal(t, "my-config", result.Name)
	assert.Equal(t, "westus", result.Location)
}

func Test_AzureClient_PurgeAppConfig_Coverage3(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	client := newAzureClientFromMockContext(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodPost &&
			strings.Contains(req.URL.Path, "/purge")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(req, http.StatusOK)
	})

	err := client.PurgeAppConfig(*mockCtx.Context, "SUB", "my-config", "westus")
	require.NoError(t, err)
}

// --- Log Analytics ---

func Test_AzureClient_GetLogAnalyticsWorkspace_Coverage3(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	client := newAzureClientFromMockContext(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			strings.Contains(req.URL.Path, "/workspaces/my-workspace")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armoperationalinsights.Workspace{
				ID:       to.Ptr("/subscriptions/SUB/resourceGroups/RG/providers/Microsoft.OperationalInsights/workspaces/my-workspace"),
				Name:     to.Ptr("my-workspace"),
				Location: to.Ptr("eastus"),
			})
	})

	result, err := client.GetLogAnalyticsWorkspace(*mockCtx.Context, "SUB", "RG", "my-workspace")
	require.NoError(t, err)
	assert.Equal(t, "my-workspace", result.Name)
	assert.Contains(t, result.Id, "my-workspace")
}

func Test_AzureClient_PurgeLogAnalyticsWorkspace_Coverage3(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
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

// --- Managed HSM ---

func Test_AzureClient_GetManagedHSM_Coverage3(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	client := newAzureClientFromMockContext(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			strings.Contains(req.URL.Path, "/managedHSMs/my-hsm")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armkeyvault.ManagedHsm{
				ID:       to.Ptr("/subscriptions/SUB/resourceGroups/RG/providers/Microsoft.KeyVault/managedHSMs/my-hsm"),
				Name:     to.Ptr("my-hsm"),
				Location: to.Ptr("eastus"),
				Properties: &armkeyvault.ManagedHsmProperties{
					EnableSoftDelete:      to.Ptr(true),
					EnablePurgeProtection: to.Ptr(false),
				},
			})
	})

	result, err := client.GetManagedHSM(*mockCtx.Context, "SUB", "RG", "my-hsm")
	require.NoError(t, err)
	assert.Equal(t, "my-hsm", result.Name)
	assert.Equal(t, "eastus", result.Location)
}

func Test_AzureClient_PurgeManagedHSM_Coverage3(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	client := newAzureClientFromMockContext(mockCtx)

	pollURL := "https://management.azure.com/subscriptions/SUB/providers/Microsoft.KeyVault/locations/eastus/operationResults/op123?api-version=2023-07-01"

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

// --- WebApp ---

func Test_AzureClient_GetAppServiceProperties_Coverage3(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	client := newAzureClientFromMockContext(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			strings.Contains(req.URL.Path, "/Microsoft.Web/sites/my-app") &&
			!strings.Contains(req.URL.Path, "/slots/")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armappservice.Site{
				ID:       to.Ptr("/subscriptions/SUB/resourceGroups/RG/providers/Microsoft.Web/sites/my-app"),
				Name:     to.Ptr("my-app"),
				Location: to.Ptr("eastus"),
				Kind:     to.Ptr("app,linux"),
				Properties: &armappservice.SiteProperties{
					DefaultHostName:    to.Ptr("my-app.azurewebsites.net"),
					HTTPSOnly:          to.Ptr(true),
					EnabledHostNames:   []*string{to.Ptr("my-app.azurewebsites.net")},
					HostNameSSLStates:  []*armappservice.HostNameSSLState{},
					SiteConfig:         &armappservice.SiteConfig{LinuxFxVersion: to.Ptr("NODE|18-lts")},
					AvailabilityState:  to.Ptr(armappservice.SiteAvailabilityStateNormal),
				},
			})
	})

	props, err := client.GetAppServiceProperties(*mockCtx.Context, "SUB", "RG", "my-app")
	require.NoError(t, err)
	assert.Contains(t, props.HostNames, "my-app.azurewebsites.net")
}

func Test_AzureClient_GetAppServiceSlotProperties_Coverage3(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	client := newAzureClientFromMockContext(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			strings.Contains(req.URL.Path, "/slots/staging")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armappservice.Site{
				ID:       to.Ptr("/subscriptions/SUB/resourceGroups/RG/providers/Microsoft.Web/sites/my-app/slots/staging"),
				Name:     to.Ptr("my-app/staging"),
				Location: to.Ptr("eastus"),
				Kind:     to.Ptr("app,linux"),
				Properties: &armappservice.SiteProperties{
					DefaultHostName:    to.Ptr("my-app-staging.azurewebsites.net"),
					HTTPSOnly:          to.Ptr(true),
					EnabledHostNames:   []*string{to.Ptr("my-app-staging.azurewebsites.net")},
					HostNameSSLStates:  []*armappservice.HostNameSSLState{},
					SiteConfig:         &armappservice.SiteConfig{LinuxFxVersion: to.Ptr("NODE|18-lts")},
					AvailabilityState:  to.Ptr(armappservice.SiteAvailabilityStateNormal),
				},
			})
	})

	props, err := client.GetAppServiceSlotProperties(*mockCtx.Context, "SUB", "RG", "my-app", "staging")
	require.NoError(t, err)
	assert.Contains(t, props.HostNames, "my-app-staging.azurewebsites.net")
}
