// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/apimanagement/armapimanagement"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appconfiguration/armappconfiguration"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	. "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazcli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBicepPlan(t *testing.T) {
	progressLog := []string{}
	interactiveLog := []bool{}
	progressDone := make(chan bool)

	mockContext := mocks.NewMockContext(context.Background())
	prepareBicepMocks(mockContext)
	infraProvider := createBicepProvider(t, mockContext)
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
	require.Contains(t, progressLog[0], "Generating Bicep parameters file")
	require.Contains(t, progressLog[1], "Compiling Bicep template")

	require.IsType(t, BicepDeploymentDetails{}, deploymentPlan.Details)
	configuredParameters := deploymentPlan.Details.(BicepDeploymentDetails).Parameters

	require.Equal(t, infraProvider.env.Values["AZURE_LOCATION"], configuredParameters["location"].Value)
	require.Equal(
		t,
		infraProvider.env.Values["AZURE_ENV_NAME"],
		configuredParameters["environmentName"].Value,
	)
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

func TestBicepPlanPrompt(t *testing.T) {
	progressLog := []string{}
	interactiveLog := []bool{}
	progressDone := make(chan bool)

	mockContext := mocks.NewMockContext(context.Background())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(args.Cmd, "bicep") && args.Args[0] == "--version"
	}).Respond(exec.RunResult{
		Stdout: "Bicep CLI version 0.12.40 (41892bd0fb)",
		Stderr: "",
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(args.Cmd, "bicep") && args.Args[0] == "build"
	}).Respond(exec.RunResult{
		Stdout: paramsArmJson,
		Stderr: "",
	})

	mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "for the 'stringParam' infrastructure parameter")
	}).Respond("value")

	mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "Save the value in the environment for future use")
	}).Respond(false)

	infraProvider := createBicepProvider(t, mockContext)
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

	plan, err := planningTask.Await()
	<-progressDone

	require.NoError(t, err)

	bicepDetails := plan.Details.(BicepDeploymentDetails)

	require.Equal(t, "value", bicepDetails.Parameters["stringParam"].Value)
}

func TestBicepState(t *testing.T) {
	progressLog := []string{}
	interactiveLog := []bool{}
	progressDone := make(chan bool)
	expectedWebsiteUrl := "http://myapp.azurewebsites.net"

	mockContext := mocks.NewMockContext(context.Background())
	prepareBicepMocks(mockContext)
	prepareStateMocks(mockContext)

	infraProvider := createBicepProvider(t, mockContext)
	getDeploymentTask := infraProvider.State(*mockContext.Context)

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
	require.Contains(t, progressLog[0], "Loading Bicep template")
	require.Contains(t, progressLog[1], "Retrieving Azure deployment")
	require.Contains(t, progressLog[2], "Normalizing output parameters")
}

func TestBicepDeploy(t *testing.T) {
	expectedWebsiteUrl := "http://myapp.azurewebsites.net"
	progressLog := []string{}
	interactiveLog := []bool{}
	progressDone := make(chan bool)

	mockContext := mocks.NewMockContext(context.Background())
	prepareBicepMocks(mockContext)
	prepareStateMocks(mockContext)
	prepareDeployMocks(mockContext)
	azCli := mockazcli.NewAzCliFromMockContext(mockContext)

	infraProvider := createBicepProvider(t, mockContext)

	deploymentPlan := DeploymentPlan{
		Deployment: Deployment{},
		Details: BicepDeploymentDetails{
			Template:   azure.RawArmTemplate("{}"),
			Parameters: testArmParameters,
			Target: infra.NewSubscriptionDeployment(
				azCli,
				infraProvider.env.Values["AZURE_LOCATION"],
				infraProvider.env.GetSubscriptionId(),
				infraProvider.env.GetEnvName(),
			),
		},
	}

	deployTask := infraProvider.Deploy(*mockContext.Context, &deploymentPlan)

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

func TestBicepDestroy(t *testing.T) {
	t.Run("Interactive", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		prepareBicepMocks(mockContext)
		prepareStateMocks(mockContext)
		prepareDestroyMocks(mockContext)

		progressLog := []string{}
		interactiveLog := []bool{}
		progressDone := make(chan bool)

		// Setup console mocks
		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "are you sure you want to continue")
		}).Respond(true)

		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(
				options.Message,
				"Would you like to permanently delete these resources instead",
			)
		}).Respond(true)

		infraProvider := createBicepProvider(t, mockContext)
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
		require.Len(t, consoleOutput, 8)
		require.Contains(t, consoleOutput[0], "Resource group(s) to be deleted")
		require.Contains(t, consoleOutput[1], "Total resources to delete")
		require.Contains(t, consoleOutput[2], "Deleting your resources can take some time")
		require.Contains(t, consoleOutput[3], "")
		require.Contains(t, consoleOutput[4], "Warning: The following operation will delete")
		require.Contains(t, consoleOutput[5], "These resources have soft delete enabled allowing")
		require.Contains(t, consoleOutput[6], "Would you like to permanently delete these resources instead")
		require.Contains(t, consoleOutput[7], "")

		// Verify progress output
		require.Len(t, progressLog, 5)
		require.Contains(t, progressLog[0], "Fetching resource groups")
		require.Contains(t, progressLog[1], "Fetching resources")
		require.Contains(t, progressLog[2], "Getting Key Vaults to purge")
		require.Contains(t, progressLog[3], "Getting App Configurations to purge")
		require.Contains(t, progressLog[4], "Getting API Management Services to purge")
	})

	t.Run("InteractiveForceAndPurge", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		prepareBicepMocks(mockContext)
		prepareStateMocks(mockContext)
		prepareDestroyMocks(mockContext)

		progressLog := []string{}
		interactiveLog := []bool{}
		progressDone := make(chan bool)

		infraProvider := createBicepProvider(t, mockContext)
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
		require.Len(t, consoleOutput, 2)
		require.Contains(t, consoleOutput[0], "Deleting your resources can take some time")
		require.Contains(t, consoleOutput[1], "")

		// Verify progress output
		require.Len(t, progressLog, 5)
		require.Contains(t, progressLog[0], "Fetching resource groups")
		require.Contains(t, progressLog[1], "Fetching resources")
		require.Contains(t, progressLog[2], "Getting Key Vaults to purge")
		require.Contains(t, progressLog[3], "Getting App Configurations to purge")
		require.Contains(t, progressLog[4], "Getting API Management Services to purge")
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

type testBicep struct {
	commandRunner exec.CommandRunner
}

func (b *testBicep) Build(ctx context.Context, file string) (string, error) {
	result, err := b.commandRunner.Run(ctx, exec.NewRunArgs("bicep", ([]string{"build", file, "--stdout"})...))
	if err != nil {
		return "", err
	}
	return result.Stdout, nil
}

func createBicepProvider(t *testing.T, mockContext *mocks.MockContext) *BicepProvider {
	projectDir := "../../../../test/functional/testdata/samples/webapp"
	options := Options{
		Module: "main",
	}

	env := environment.EphemeralWithValues("test-env", map[string]string{
		environment.LocationEnvVarName:       "westus2",
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})

	azCli := mockazcli.NewAzCliFromMockContext(mockContext)

	provider := &BicepProvider{
		env:         env,
		projectPath: projectDir,
		options:     options,
		console:     mockContext.Console,
		bicepCli: &testBicep{
			commandRunner: mockContext.CommandRunner,
		},
		azCli: azCli,
		prompters: Prompters{
			Location: func(_ context.Context, _ string, _ string, _ func(loc account.Location) bool) (string, error) {
				return "westus2", nil
			},
			Subscription: func(_ context.Context, _ string) (subscriptionId string, err error) {
				return "SUBSCRIPTION_ID", nil
			},
			EnsureSubscriptionLocation: func(ctx context.Context, env *environment.Environment) error {
				env.SetSubscriptionId("SUBSCRIPTION_ID")
				env.SetLocation("westus2")
				return nil
			},
		},
		curPrincipal: &mockCurrentPrincipal{},
	}

	return provider
}

func prepareBicepMocks(
	mockContext *mocks.MockContext) {

	armTemplate := azure.ArmTemplate{
		Schema:         "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
		ContentVersion: "1.0.0.0",
		Parameters: azure.ArmTemplateParameterDefinitions{
			"environmentName": {Type: "string"},
			"location":        {Type: "string"},
		},
		Outputs: azure.ArmTemplateOutputs{
			"WEBSITE_URL": {Type: "string"},
		},
	}

	bicepBytes, _ := json.Marshal(armTemplate)

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(args.Cmd, "bicep") && args.Args[0] == "--version"
	}).Respond(exec.RunResult{
		Stdout: "Bicep CLI version 0.12.40 (41892bd0fb)",
		Stderr: "",
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(args.Cmd, "bicep") && args.Args[0] == "build"
	}).Respond(exec.RunResult{
		Stdout: string(bicepBytes),
		Stderr: "",
	})
}

var cTestEnvDeployment armresources.DeploymentExtended = armresources.DeploymentExtended{
	ID:   convert.RefOf("DEPLOYMENT_ID"),
	Name: convert.RefOf("test-env"),
	Properties: &armresources.DeploymentPropertiesExtended{
		Outputs: map[string]interface{}{
			"WEBSITE_URL": map[string]interface{}{"value": "http://myapp.azurewebsites.net", "type": "string"},
		},
		OutputResources: []*armresources.ResourceReference{
			{
				ID: to.Ptr("/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP"),
			},
		},
		ProvisioningState: to.Ptr(armresources.ProvisioningStateSucceeded),
		Timestamp:         to.Ptr(time.Now()),
	},
}

func prepareDeployMocks(mockContext *mocks.MockContext) {
	deployResultBytes, _ := json.Marshal(cTestEnvDeployment)

	// Create deployment at subscription scope
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPut && strings.HasSuffix(
			request.URL.Path,
			"/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/test-env",
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer(deployResultBytes)),
			Request: &http.Request{
				Method: http.MethodGet,
			},
		}, nil
	})

	deploymentsPage := &armresources.DeploymentListResult{
		Value: []*armresources.DeploymentExtended{
			&cTestEnvDeployment,
		},
	}

	deploymentsPageResultBytes, _ := json.Marshal(deploymentsPage)

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.HasSuffix(
			request.URL.Path,
			"/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/",
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer(deploymentsPageResultBytes)),
		}, nil
	})
}

func prepareStateMocks(mockContext *mocks.MockContext) {
	deployResultBytes, _ := json.Marshal(cTestEnvDeployment)

	// Get deployment result
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.HasSuffix(
			request.URL.Path,
			"/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/test-env",
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer(deployResultBytes)),
		}, nil
	})

	deploymentsPage := &armresources.DeploymentListResult{
		Value: []*armresources.DeploymentExtended{
			&cTestEnvDeployment,
		},
	}

	deploymentsPageResultBytes, _ := json.Marshal(deploymentsPage)

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.HasSuffix(
			request.URL.Path,
			"/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/",
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer(deploymentsPageResultBytes)),
		}, nil
	})
}

func prepareDestroyMocks(mockContext *mocks.MockContext) {
	makeItem := func(resourceType infra.AzureResourceType, resourceName string) *armresources.GenericResourceExpanded {
		id := fmt.Sprintf("subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP/%s/%s",
			string(resourceType), resourceName)

		return &armresources.GenericResourceExpanded{
			ID:       convert.RefOf(id),
			Name:     convert.RefOf(resourceName),
			Type:     convert.RefOf(string(resourceType)),
			Location: convert.RefOf("eastus2"),
		}
	}

	resourceList := armresources.ResourceListResult{
		Value: []*armresources.GenericResourceExpanded{
			makeItem(infra.AzureResourceTypeWebSite, "app-123"),
			makeItem(infra.AzureResourceTypeKeyVault, "kv-123"),
			makeItem(infra.AzureResourceTypeKeyVault, "kv2-123"),
			makeItem(infra.AzureResourceTypeAppConfig, "ac-123"),
			makeItem(infra.AzureResourceTypeAppConfig, "ac2-123"),
			makeItem(infra.AzureResourceTypeApim, "apim-123"),
			makeItem(infra.AzureResourceTypeApim, "apim2-123"),
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
			strings.HasSuffix(request.URL.Path, "subscriptions/SUBSCRIPTION_ID/resourcegroups/RESOURCE_GROUP")
	}).RespondFn(httpRespondFn)

	// Purge Key vault
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost &&
			(strings.HasSuffix(request.URL.Path, "deletedVaults/kv-123/purge") ||
				strings.HasSuffix(request.URL.Path, "deletedVaults/kv2-123/purge"))
	}).RespondFn(httpRespondFn)

	// Purge App configuration
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost &&
			(strings.HasSuffix(request.URL.Path, "deletedConfigurationStores/ac-123/purge") ||
				strings.HasSuffix(request.URL.Path, "deletedConfigurationStores/ac2-123/purge"))
	}).RespondFn(httpRespondFn)

	// Purge APIM
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodDelete &&
			(strings.HasSuffix(request.URL.Path, "deletedservices/apim-123") ||
				strings.HasSuffix(request.URL.Path, "deletedservices/apim2-123"))
	}).RespondFn(httpRespondFn)

	// Delete deployment
	mockPollingUrl := "https://url-to-poll.net/keep-deleting"
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodDelete &&
			strings.HasSuffix(
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
		return request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, keyVaultString)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		keyVaultResponse := armkeyvault.VaultsClientGetResponse{
			Vault: armkeyvault.Vault{
				ID: convert.RefOf(
					fmt.Sprintf("/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP/%s/%s",
						string(infra.AzureResourceTypeKeyVault), name)),
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
		return request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, appConfigString)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		appConfigResponse := armappconfiguration.ConfigurationStoresClientGetResponse{
			ConfigurationStore: armappconfiguration.ConfigurationStore{
				ID: convert.RefOf(
					fmt.Sprintf("/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP/%s/%s",
						string(infra.AzureResourceTypeAppConfig), name)),

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
		return request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, apimString)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		apimResponse := armapimanagement.ServiceClientGetResponse{
			ServiceResource: armapimanagement.ServiceResource{
				ID: convert.RefOf(
					fmt.Sprintf("/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP/%s/%s",
						string(infra.AzureResourceTypeApim), name)),

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

func TestResourceGroupsFromLatestDeployment(t *testing.T) {
	t.Run("duplicate resource groups ignored", func(t *testing.T) {

		mockContext := mocks.NewMockContext(context.Background())

		mockDeployment := armresources.DeploymentExtended{
			ID:   convert.RefOf("DEPLOYMENT_ID"),
			Name: convert.RefOf("test-env"),
			Properties: &armresources.DeploymentPropertiesExtended{
				OutputResources: []*armresources.ResourceReference{
					{
						ID: convert.RefOf("/subscriptions/sub-id/resourceGroups/groupA"),
					},
					{
						ID: convert.RefOf("/subscriptions/sub-id/resourceGroups/groupA/Microsoft.Storage/storageAccounts/storageAccount"),
					},
					{
						ID: convert.RefOf("/subscriptions/sub-id/resourceGroups/groupB"),
					},
					{
						ID: convert.RefOf("/subscriptions/sub-id/resourceGroups/groupB/Microsoft.web/sites/test"),
					},
					{
						ID: convert.RefOf("/subscriptions/sub-id/resourceGroups/groupC"),
					},
				},
				ProvisioningState: to.Ptr(armresources.ProvisioningStateSucceeded),
				Timestamp:         to.Ptr(time.Now()),
			},
		}

		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet && strings.HasSuffix(
				request.URL.Path,
				"/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/",
			)
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			subscriptionsListBytes, _ := json.Marshal(
				armresources.DeploymentsClientListAtSubscriptionScopeResponse{
					DeploymentListResult: armresources.DeploymentListResult{
						Value: []*armresources.DeploymentExtended{&mockDeployment},
					},
				})

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBuffer(subscriptionsListBytes)),
			}, nil
		})

		infraProvider := createBicepProvider(t, mockContext)
		groups, err := infraProvider.getResourceGroupsFromLatestDeployment(*mockContext.Context)

		require.NoError(t, err)

		sort.Strings(groups)
		require.Equal(t, []string{"groupA", "groupB", "groupC"}, groups)
	})
}
