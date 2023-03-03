// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package arm

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/apimanagement/armapimanagement"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appconfiguration/armappconfiguration"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	. "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazcli"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockhttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArmPlan(t *testing.T) {
	progressLog := []string{}
	interactiveLog := []bool{}
	progressDone := make(chan bool)

	mockContext := mocks.NewMockContext(context.Background())
	infraProvider := createArmProvider(t, mockContext)
	planningTask := infraProvider.Plan(*mockContext.Context)

	go func() {
		for progressReport := range planningTask.Progress() {
			progressLog = append(progressLog, progressReport.Message)
		}
		progressDone <- true
	}()

	go func() {
		for planningInteractive := range planningTask.Interactive() {
			interactiveLog = append(interactiveLog, planningInteractive)
		}
	}()

	deploymentPlan, err := planningTask.Await()
	<-progressDone

	require.Nil(t, err)
	require.NotNil(t, deploymentPlan.Deployment)

	require.Len(t, progressLog, 2)
	require.Contains(t, progressLog[0], "Generating Arm parameters file")
	require.Contains(t, progressLog[1], "Compiling Arm template")
}

const paramsArmJson = `{
	"$schema": "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
	"contentVersion": "1.0.0.0",
	"parameters": {
	  "stringParam": {
		"type": "string",
		"metadata": {
		  "description": "A required string parameter"
		}
	  }
	},
	"resources": [],
	"outputs": {}
  }`

func TestArmState(t *testing.T) {
	progressLog := []string{}
	interactiveLog := []bool{}
	progressDone := make(chan bool)
	expectedWebsiteUrl := "http://myapp.azurewebsites.net"

	mockContext := mocks.NewMockContext(context.Background())
	prepareDeployShowMocks(mockContext.HttpClient)
	azCli := mockazcli.NewAzCliFromMockContext(mockContext)

	infraProvider := createArmProvider(t, mockContext)
	scope := infra.NewSubscriptionScope(
		azCli,
		infraProvider.env.Values["AZURE_LOCATION"],
		infraProvider.env.GetSubscriptionId(),
		infraProvider.env.GetEnvName(),
	)
	getDeploymentTask := infraProvider.State(*mockContext.Context, scope)

	go func() {
		for progressReport := range getDeploymentTask.Progress() {
			progressLog = append(progressLog, progressReport.Message)
		}
		progressDone <- true
	}()

	go func() {
		for deploymentInteractive := range getDeploymentTask.Interactive() {
			interactiveLog = append(interactiveLog, deploymentInteractive)
		}
	}()

	getDeploymentResult, err := getDeploymentTask.Await()
	<-progressDone

	require.Nil(t, err)
	require.NotNil(t, getDeploymentResult.State)
	require.Equal(t, getDeploymentResult.State.Outputs["WEBSITE_URL"].Value, expectedWebsiteUrl)

	require.Len(t, progressLog, 3)
	require.Contains(t, progressLog[0], "Loading Arm template")
	require.Contains(t, progressLog[1], "Retrieving Azure deployment")
	require.Contains(t, progressLog[2], "Normalizing output parameters")
}

func TestArmDeploy(t *testing.T) {
	expectedWebsiteUrl := "http://myapp.azurewebsites.net"
	progressLog := []string{}
	interactiveLog := []bool{}
	progressDone := make(chan bool)

	mockContext := mocks.NewMockContext(context.Background())
	prepareDeployShowMocks(mockContext.HttpClient)
	azCli := mockazcli.NewAzCliFromMockContext(mockContext)

	infraProvider := createArmProvider(t, mockContext)

	deploymentPlan := DeploymentPlan{
		Deployment: Deployment{},
		Details: ArmDeploymentDetails{
			Template: azure.RawArmTemplate("{}"),
		},
	}

	scope := infra.NewSubscriptionScope(
		azCli,
		infraProvider.env.Values["AZURE_LOCATION"],
		infraProvider.env.GetSubscriptionId(),
		infraProvider.env.GetEnvName(),
	)
	deployTask := infraProvider.Deploy(*mockContext.Context, &deploymentPlan, scope)

	go func() {
		for deployProgress := range deployTask.Progress() {
			progressLog = append(progressLog, deployProgress.Message)
		}
		progressDone <- true
	}()

	go func() {
		for deployInteractive := range deployTask.Interactive() {
			interactiveLog = append(interactiveLog, deployInteractive)
		}
	}()

	deployResult, err := deployTask.Await()
	<-progressDone

	require.Nil(t, err)
	require.NotNil(t, deployResult)
	require.Equal(t, deployResult.Deployment.Outputs["WEBSITE_URL"].Value, expectedWebsiteUrl)
}

func TestArmDestroy(t *testing.T) {
	t.Run("Interactive", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		prepareDeployShowMocks(mockContext.HttpClient)
		prepareDestroyMocks(mockContext)

		progressLog := []string{}
		interactiveLog := []bool{}
		progressDone := make(chan bool)

		// Setup console mocks
		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "This will delete")
		}).Respond(true)

		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(
				options.Message,
				"Would you like to permanently delete these Key Vaults/App Configurations",
			)
		}).Respond(true)

		infraProvider := createArmProvider(t, mockContext)
		deployment := Deployment{}

		destroyOptions := NewDestroyOptions(false, false)
		destroyTask := infraProvider.Destroy(*mockContext.Context, &deployment, destroyOptions)

		go func() {
			for destroyProgress := range destroyTask.Progress() {
				progressLog = append(progressLog, destroyProgress.Message)
			}
			progressDone <- true
		}()

		go func() {
			for destroyInteractive := range destroyTask.Interactive() {
				interactiveLog = append(interactiveLog, destroyInteractive)
			}
		}()

		destroyResult, err := destroyTask.Await()
		<-progressDone

		require.Nil(t, err)
		require.NotNil(t, destroyResult)

		// Verify console prompts
		consoleOutput := mockContext.Console.Output()
		require.Len(t, consoleOutput, 11)
		require.Contains(t, consoleOutput[0], "This will delete")
		require.Contains(t, consoleOutput[1], "Deleted resource group")
		require.Contains(t, consoleOutput[2], "This operation will delete")
		require.Contains(t, consoleOutput[3], "Would you like to permanently delete these Key Vaults/App Configurations")
		require.Contains(t, consoleOutput[4], "Purged key vault kv-123")
		require.Contains(t, consoleOutput[5], "Purged key vault kv2-123")
		require.Contains(t, consoleOutput[6], "Purged app configuration ac-123")
		require.Contains(t, consoleOutput[7], "Purged app configuration ac2-123")
		require.Contains(t, consoleOutput[8], "Purged api management service apim-123")
		require.Contains(t, consoleOutput[9], "Purged api management service apim2-123")
		require.Contains(t, consoleOutput[10], "Deleted deployment")

		// Verify progress output
		require.Len(t, progressLog, 13)
		require.Contains(t, progressLog[0], "Fetching resource groups")
		require.Contains(t, progressLog[1], "Fetching resources")
		require.Contains(t, progressLog[2], "Getting Key Vaults to purge")
		require.Contains(t, progressLog[3], "Getting App Configurations to purge")
		require.Contains(t, progressLog[4], "Getting API Management Services to purge")
		require.Contains(t, progressLog[5], "Deleting resource group")
		require.Contains(t, progressLog[6], "Purging key vault kv-123")
		require.Contains(t, progressLog[7], "Purging key vault kv2-123")
		require.Contains(t, progressLog[8], "Purging app configuration ac-123")
		require.Contains(t, progressLog[9], "Purging app configuration ac2-123")
		require.Contains(t, progressLog[10], "Purging api management service apim-123")
		require.Contains(t, progressLog[11], "Purging api management service apim2-123")
		require.Contains(t, progressLog[12], "Deleting deployment")
	})

	t.Run("InteractiveForceAndPurge", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		prepareDeployShowMocks(mockContext.HttpClient)
		prepareDestroyMocks(mockContext)

		progressLog := []string{}
		interactiveLog := []bool{}
		progressDone := make(chan bool)

		infraProvider := createArmProvider(t, mockContext)
		deployment := Deployment{}

		destroyOptions := NewDestroyOptions(true, true)
		destroyTask := infraProvider.Destroy(*mockContext.Context, &deployment, destroyOptions)

		go func() {
			for destroyProgress := range destroyTask.Progress() {
				progressLog = append(progressLog, destroyProgress.Message)
			}
			progressDone <- true
		}()

		go func() {
			for destroyInteractive := range destroyTask.Interactive() {
				interactiveLog = append(interactiveLog, destroyInteractive)
			}
		}()

		destroyResult, err := destroyTask.Await()
		<-progressDone

		require.Nil(t, err)
		require.NotNil(t, destroyResult)

		// Verify console prompts
		consoleOutput := mockContext.Console.Output()
		require.Len(t, consoleOutput, 8)
		require.Contains(t, consoleOutput[0], "Deleted resource group")
		require.Contains(t, consoleOutput[1], "Purged key vault kv-123")
		require.Contains(t, consoleOutput[2], "Purged key vault kv2-123")
		require.Contains(t, consoleOutput[3], "Purged app configuration ac-123")
		require.Contains(t, consoleOutput[4], "Purged app configuration ac2-123")
		require.Contains(t, consoleOutput[5], "Purged api management service apim-123")
		require.Contains(t, consoleOutput[6], "Purged api management service apim2-123")
		require.Contains(t, consoleOutput[7], "Deleted deployment")

		// Verify progress output
		require.Len(t, progressLog, 13)
		require.Contains(t, progressLog[0], "Fetching resource groups")
		require.Contains(t, progressLog[1], "Fetching resources")
		require.Contains(t, progressLog[2], "Getting Key Vaults to purge")
		require.Contains(t, progressLog[3], "Getting App Configurations to purge")
		require.Contains(t, progressLog[4], "Getting API Management Services to purge")
		require.Contains(t, progressLog[5], "Deleting resource group")
		require.Contains(t, progressLog[6], "Purging key vault kv-123")
		require.Contains(t, progressLog[7], "Purging key vault kv2-123")
		require.Contains(t, progressLog[8], "Purging app configuration ac-123")
		require.Contains(t, progressLog[9], "Purging app configuration ac2-123")
		require.Contains(t, progressLog[10], "Purging api management service apim-123")
		require.Contains(t, progressLog[11], "Purging api management service apim2-123")
		require.Contains(t, progressLog[12], "Deleting deployment")

	})
}

func TestIsValueAssignableToParameterType(t *testing.T) {
	cases := map[ParameterType]any{
		ParameterTypeNumber:  1,
		ParameterTypeBoolean: true,
		ParameterTypeString:  "hello",
		ParameterTypeArray:   []any{},
		ParameterTypeObject:  map[string]any{},
	}

	for k := range cases {
		assert.True(t, isValueAssignableToParameterType(k, cases[k]), "%v should be assignable to %v", cases[k], k)

		for j := range cases {
			if j != k {
				assert.False(
					t, isValueAssignableToParameterType(k, cases[j]), "%v should not be assignable to %v", cases[j], k,
				)
			}
		}
	}

	assert.True(t, isValueAssignableToParameterType(ParameterTypeNumber, 1.0))
	assert.True(t, isValueAssignableToParameterType(ParameterTypeNumber, json.Number("1")))
	assert.False(t, isValueAssignableToParameterType(ParameterTypeNumber, 1.5))
	assert.False(t, isValueAssignableToParameterType(ParameterTypeNumber, json.Number("1.5")))
}

func createArmProvider(t *testing.T, mockContext *mocks.MockContext) *ArmProvider {
	projectDir := "../../../../test/functional/testdata/samples/webapp"
	options := Options{
		Provider: "arm",
		Module:   "main",
	}

	env := environment.EphemeralWithValues("test-env", map[string]string{
		environment.LocationEnvVarName:       "westus2",
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})

	azCli := azcli.NewAzCli(mockContext.Credentials, azcli.NewAzCliArgs{
		HttpClient: mockContext.HttpClient,
	})

	locationPrompter := func(msg string, filter func(loc azcli.AzCliLocation) bool) (location string, err error) {
		return "", nil
	}

	provider, err := NewArmProvider(
		*mockContext.Context, azCli, env, projectDir, options,
		mockContext.CommandRunner,
		mockContext.Console,
		Prompters{
			Location: locationPrompter,
		},
	)

	require.NoError(t, err)
	return provider
}

func prepareDeployShowMocks(
	httpClient *mockhttp.MockHttpClient) {
	expectedWebsiteUrl := "http://myapp.azurewebsites.net"

	deployOutputs := make(map[string]interface{})
	deployOutputs["WEBSITE_URL"] = map[string]interface{}{"value": expectedWebsiteUrl, "type": "string"}
	azDeployment := armresources.DeploymentExtended{
		ID:   convert.RefOf("DEPLOYMENT_ID"),
		Name: convert.RefOf("DEPLOYMENT_NAME"),
		Properties: &armresources.DeploymentPropertiesExtended{
			Outputs: deployOutputs,
			Dependencies: []*armresources.Dependency{
				{
					DependsOn: []*armresources.BasicDependency{
						{
							ID:           convert.RefOf("RESOURCE_ID"),
							ResourceName: convert.RefOf("RESOURCE_GROUP"),
							ResourceType: convert.RefOf(string(infra.AzureResourceTypeResourceGroup)),
						},
					},
				},
			},
		},
	}

	deployResultBytes, _ := json.Marshal(azDeployment)

	// Get deployment result
	httpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(
			request.URL.Path,
			"/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments",
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer(deployResultBytes)),
		}, nil
	})
}

func prepareDestroyMocks(mockContext *mocks.MockContext) {
	resourceList := armresources.ResourceListResult{
		Value: []*armresources.GenericResourceExpanded{
			{
				ID:       convert.RefOf("webapp"),
				Name:     convert.RefOf("app-123"),
				Type:     convert.RefOf(string(infra.AzureResourceTypeWebSite)),
				Location: convert.RefOf("eastus2"),
			},
			{
				ID:       convert.RefOf("keyvault"),
				Name:     convert.RefOf("kv-123"),
				Type:     convert.RefOf(string(infra.AzureResourceTypeKeyVault)),
				Location: convert.RefOf("eastus2"),
			},
			{
				ID:       convert.RefOf("keyvault2"),
				Name:     convert.RefOf("kv2-123"),
				Type:     convert.RefOf(string(infra.AzureResourceTypeKeyVault)),
				Location: convert.RefOf("eastus2"),
			},
			{
				ID:       convert.RefOf("appconfiguration"),
				Name:     convert.RefOf("ac-123"),
				Type:     convert.RefOf(string(infra.AzureResourceTypeAppConfig)),
				Location: convert.RefOf("eastus2"),
			},
			{
				ID:       convert.RefOf("appconfiguration2"),
				Name:     convert.RefOf("ac2-123"),
				Type:     convert.RefOf(string(infra.AzureResourceTypeAppConfig)),
				Location: convert.RefOf("eastus2"),
			},
			{
				ID:       convert.RefOf("ApiManagement"),
				Name:     convert.RefOf("apim-123"),
				Type:     convert.RefOf(string(infra.AzureResourceTypeApim)),
				Location: convert.RefOf("eastus2"),
			},
			{
				ID:       convert.RefOf("ApiManagement"),
				Name:     convert.RefOf("apim2-123"),
				Type:     convert.RefOf(string(infra.AzureResourceTypeApim)),
				Location: convert.RefOf("eastus2"),
			},
		},
	}

	// Get list of resources to delete
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/resources")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		resourceListBytes, _ := json.Marshal(resourceList)

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer(resourceListBytes)),
		}, nil
	})

	// Get Key Vault
	getKeyVaultMock(mockContext, "/vaults/kv-123", "kv-123", "eastus2")
	getKeyVaultMock(mockContext, "/vaults/kv2-123", "kv2-123", "eastus2")

	// Get App Configuration
	getAppConfigMock(mockContext, "/configurationStores/ac-123", "ac-123", "eastus2")
	getAppConfigMock(mockContext, "/configurationStores/ac2-123", "ac2-123", "eastus2")

	// Get APIM
	getAPIMMock(mockContext, "/service/apim-123", "apim-123", "eastus2")
	getAPIMMock(mockContext, "/service/apim2-123", "apim2-123", "eastus2")

	// Delete resource group
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodDelete &&
			strings.Contains(request.URL.Path, "subscriptions/SUBSCRIPTION_ID/resourcegroups/RESOURCE_GROUP")
	}).RespondFn(httpRespondFn)

	// Purge Key vault
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost &&
			(strings.Contains(request.URL.Path, "deletedVaults/kv-123/purge") ||
				strings.Contains(request.URL.Path, "deletedVaults/kv2-123/purge"))
	}).RespondFn(httpRespondFn)

	// Purge App configuration
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost &&
			(strings.Contains(request.URL.Path, "deletedConfigurationStores/ac-123/purge") ||
				strings.Contains(request.URL.Path, "deletedConfigurationStores/ac2-123/purge"))
	}).RespondFn(httpRespondFn)

	// Purge APIM
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodDelete &&
			(strings.Contains(request.URL.Path, "deletedservices/apim-123") ||
				strings.Contains(request.URL.Path, "deletedservices/apim2-123"))
	}).RespondFn(httpRespondFn)

	// Delete deployment
	mockPollingUrl := "https://url-to-poll.net/keep-deleting"
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodDelete &&
			strings.Contains(
				request.URL.Path, "/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/test-env")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		response, err := mocks.CreateEmptyHttpResponse(request, 202)
		response.Header.Add("location", mockPollingUrl)
		return response, err
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(
				request.URL.String(), mockPollingUrl)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		// set the end of LRO with 204 response (since we are using location header)
		return mocks.CreateEmptyHttpResponse(request, 204)
	})
}

var testArmParameters = azure.ArmParameters{
	"location": {
		Value: "West US",
	},
}

func getKeyVaultMock(mockContext *mocks.MockContext, keyVaultString string, name string, location string) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, keyVaultString)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		keyVaultResponse := armkeyvault.VaultsClientGetResponse{
			Vault: armkeyvault.Vault{
				ID:       convert.RefOf(name),
				Name:     convert.RefOf(name),
				Location: convert.RefOf(location),
				Properties: &armkeyvault.VaultProperties{
					EnableSoftDelete:      convert.RefOf(true),
					EnablePurgeProtection: convert.RefOf(false),
				},
			},
		}

		keyVaultBytes, _ := json.Marshal(keyVaultResponse)

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer(keyVaultBytes)),
		}, nil
	})
}

func getAppConfigMock(mockContext *mocks.MockContext, appConfigString string, name string, location string) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, appConfigString)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		appConfigResponse := armappconfiguration.ConfigurationStoresClientGetResponse{
			ConfigurationStore: armappconfiguration.ConfigurationStore{
				ID:       convert.RefOf(name),
				Name:     convert.RefOf(name),
				Location: convert.RefOf(location),
				Properties: &armappconfiguration.ConfigurationStoreProperties{
					EnablePurgeProtection: convert.RefOf(false),
				},
			},
		}

		appConfigBytes, _ := json.Marshal(appConfigResponse)

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer(appConfigBytes)),
		}, nil
	})
}

func getAPIMMock(mockContext *mocks.MockContext, apimString string, name string, location string) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, apimString)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		apimResponse := armapimanagement.ServiceClientGetResponse{
			ServiceResource: armapimanagement.ServiceResource{
				ID:       convert.RefOf(name),
				Name:     convert.RefOf(name),
				Location: convert.RefOf(location),
			},
		}

		apimBytes, _ := json.Marshal(apimResponse)

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer(apimBytes)),
		}, nil
	})
}

func httpRespondFn(request *http.Request) (*http.Response, error) {
	return &http.Response{
		Request:    request,
		Header:     http.Header{},
		StatusCode: http.StatusOK,
		Body:       http.NoBody,
	}, nil
}
