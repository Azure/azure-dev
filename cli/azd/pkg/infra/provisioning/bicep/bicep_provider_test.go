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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/apimanagement/armapimanagement"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appconfiguration/armappconfiguration"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/keyvault"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazapi"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestBicepPlan(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	prepareBicepMocks(mockContext)
	infraProvider := createBicepProvider(t, mockContext)

	deploymentPlan, err := infraProvider.plan(*mockContext.Context)

	require.Nil(t, err)

	require.IsType(t, &deploymentDetails{}, deploymentPlan)
	configuredParameters := deploymentPlan.CompiledBicep.Parameters

	require.Equal(t, infraProvider.env.GetLocation(), configuredParameters["location"].Value)
	require.Equal(
		t,
		infraProvider.env.Name(),
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
	mockContext := mocks.NewMockContext(context.Background())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(args.Cmd, "bicep") && args.Args[0] == "--version"
	}).Respond(exec.RunResult{
		Stdout: fmt.Sprintf("Bicep CLI version %s (abcdef0123)", bicep.Version.String()),
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
	plan, err := infraProvider.plan(*mockContext.Context)

	require.NoError(t, err)

	require.Equal(t, "value", plan.CompiledBicep.Parameters["stringParam"].Value)
}

func TestBicepState(t *testing.T) {
	expectedWebsiteUrl := "http://myapp.azurewebsites.net"

	mockContext := mocks.NewMockContext(context.Background())
	prepareBicepMocks(mockContext)
	prepareStateMocks(mockContext)

	infraProvider := createBicepProvider(t, mockContext)

	getDeploymentResult, err := infraProvider.State(*mockContext.Context, nil)

	require.Nil(t, err)
	require.NotNil(t, getDeploymentResult.State)
	require.Equal(t, getDeploymentResult.State.Outputs["WEBSITE_URL"].Value, expectedWebsiteUrl)
}

func TestBicepDestroy(t *testing.T) {
	t.Run("Interactive", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		prepareBicepMocks(mockContext)
		prepareStateMocks(mockContext)
		prepareDestroyMocks(mockContext)

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

		destroyOptions := provisioning.NewDestroyOptions(false, false)
		destroyResult, err := infraProvider.Destroy(*mockContext.Context, destroyOptions)

		require.Nil(t, err)
		require.NotNil(t, destroyResult)

		// Verify console prompts
		consoleOutput := mockContext.Console.Output()
		require.Len(t, consoleOutput, 4)
	})

	t.Run("InteractiveForceAndPurge", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		prepareBicepMocks(mockContext)
		prepareStateMocks(mockContext)
		prepareDestroyMocks(mockContext)

		infraProvider := createBicepProvider(t, mockContext)

		destroyOptions := provisioning.NewDestroyOptions(true, true)
		destroyResult, err := infraProvider.Destroy(*mockContext.Context, destroyOptions)

		require.Nil(t, err)
		require.NotNil(t, destroyResult)

		// Verify console prompts
		consoleOutput := mockContext.Console.Output()
		require.Len(t, consoleOutput, 2)
		require.Contains(t, consoleOutput[0], "Deleting your resources can take some time")
		require.Contains(t, consoleOutput[1], "")
	})
}

func TestPlanForResourceGroup(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(args.Cmd, "bicep") && strings.Contains(command, "--version")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, fmt.Sprintf("Bicep CLI version %s (abcdef0123)", bicep.Version), ""), nil
	})

	// Have `bicep build` return a ARM template that targets a resource group.
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(args.Cmd, "bicep") && args.Args[0] == "build"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		armTemplate := azure.ArmTemplate{
			Schema:         "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
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

		return exec.RunResult{
			Stdout: string(bicepBytes),
		}, nil
	})

	// Mock the list resource group operation to return two existing resource groups (we expect these to be offered)
	// as choices when selecting a resource group.
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.HasSuffix(request.URL.Path, "/subscriptions/SUBSCRIPTION_ID/resourcegroups")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		body := armresources.ResourceGroupListResult{
			Value: []*armresources.ResourceGroup{
				{
					ID:       to.Ptr("/subscriptions/SUBSCRIPTION_ID/resourcegroups/existingGroup2"),
					Name:     to.Ptr("existingGroup2"),
					Type:     to.Ptr("Microsoft.Resources/resourceGroup"),
					Location: to.Ptr("eastus2"),
				},
				{
					ID:       to.Ptr("/subscriptions/SUBSCRIPTION_ID/resourcegroups/existingGroup1"),
					Name:     to.Ptr("existingGroup1"),
					Type:     to.Ptr("Microsoft.Resources/resourceGroup"),
					Location: to.Ptr("eastus2"),
				},
			},
		}

		bodyBytes, _ := json.Marshal(body)

		return &http.Response{
			Request:    request,
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer(bodyBytes)),
		}, nil
	})

	// Our test will create a new resource group, instead of using one of the existing ones, so mock that operation.
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPut &&
			strings.HasSuffix(request.URL.Path, "/subscriptions/SUBSCRIPTION_ID/resourcegroups/rg-test-env")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		body := armresources.ResourceGroup{
			ID:       to.Ptr("/subscriptions/SUBSCRIPTION_ID/resourcegroups/rg-test-env"),
			Name:     to.Ptr("rg-test-env"),
			Type:     to.Ptr("Microsoft.Resources/resourceGroup"),
			Location: to.Ptr("eastus2"),
		}

		bodyBytes, _ := json.Marshal(body)

		return &http.Response{
			Request:    request,
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer(bodyBytes)),
		}, nil
	})

	// Validate that we correctly show the selection of existing groups, but pick the option to create a new one instead.
	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Pick a resource group to use:"
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		require.Len(t, options.Options, 3)
		require.Equal(t, "Create a new resource group", options.Options[0])
		require.Equal(t, "1. existingGroup1", options.Options[1])
		require.Equal(t, "2. existingGroup2", options.Options[2])

		return 0, nil
	})

	// Validate that we are prompted for a name for the new resource group, and that a suitable default is provided based
	// our current environment name.
	mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return options.Message == "Enter a name for the new resource group:"
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		require.Equal(t, "rg-test-env", options.DefaultValue)
		return options.DefaultValue, nil
	})

	infraProvider := createBicepProvider(t, mockContext)
	// The computed plan should target the resource group we picked.

	planResult, err := infraProvider.plan(*mockContext.Context)
	require.Nil(t, err)
	require.NotNil(t, planResult)
	require.Equal(t, "rg-test-env",
		planResult.Target.(*infra.ResourceGroupDeployment).ResourceGroupName())
}

func TestIsValueAssignableToParameterType(t *testing.T) {
	cases := map[provisioning.ParameterType]any{
		provisioning.ParameterTypeNumber:  1,
		provisioning.ParameterTypeBoolean: true,
		provisioning.ParameterTypeString:  "hello",
		provisioning.ParameterTypeArray:   []any{},
		provisioning.ParameterTypeObject:  map[string]any{},
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

	assert.True(t, isValueAssignableToParameterType(provisioning.ParameterTypeNumber, 1.0))
	assert.True(t, isValueAssignableToParameterType(provisioning.ParameterTypeNumber, json.Number("1")))
	assert.False(t, isValueAssignableToParameterType(provisioning.ParameterTypeNumber, 1.5))
	assert.False(t, isValueAssignableToParameterType(provisioning.ParameterTypeNumber, json.Number("1.5")))
}

func createBicepProvider(t *testing.T, mockContext *mocks.MockContext) *BicepProvider {
	projectDir := "../../../../test/functional/testdata/samples/webapp"
	options := provisioning.Options{
		Path:   "infra",
		Module: "main",
	}

	env := environment.NewWithValues("test-env", map[string]string{
		environment.LocationEnvVarName:       "westus2",
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
		environment.EnvNameEnvVarName:        "test-env",
	})

	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", mock.Anything, mock.Anything).Return(nil)

	bicepCli, err := bicep.NewCli(*mockContext.Context, mockContext.Console, mockContext.CommandRunner)
	require.NoError(t, err)
	azCli := mockazapi.NewAzureClientFromMockContext(mockContext)
	resourceService := azapi.NewResourceService(mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions)
	deploymentService := mockazapi.NewStandardDeploymentsFromMockContext(mockContext)
	resourceManager := infra.NewAzureResourceManager(resourceService, deploymentService)
	deploymentManager := infra.NewDeploymentManager(deploymentService, resourceManager, mockContext.Console)
	accountManager := &mockaccount.MockAccountManager{
		Subscriptions: []account.Subscription{
			{
				Id:   "00000000-0000-0000-0000-000000000000",
				Name: "test",
			},
		},
		Locations: []account.Location{
			{
				Name:                "location",
				DisplayName:         "Test Location",
				RegionalDisplayName: "(US) Test Location",
			},
		},
	}

	provider := NewBicepProvider(
		azCli,
		bicepCli,
		resourceService,
		deploymentManager,
		envManager,
		env,
		mockContext.Console,
		prompt.NewDefaultPrompter(env, mockContext.Console, accountManager, resourceService, cloud.AzurePublic()),
		&mockCurrentPrincipal{},
		keyvault.NewKeyVaultService(
			mockaccount.SubscriptionCredentialProviderFunc(
				func(_ context.Context, _ string) (azcore.TokenCredential, error) {
					return mockContext.Credentials, nil
				}),
			mockContext.ArmClientOptions,
			mockContext.CoreClientOptions,
		),
		cloud.AzurePublic(),
	)

	err = provider.Initialize(*mockContext.Context, projectDir, options)
	require.NoError(t, err)

	return provider.(*BicepProvider)
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
		Stdout: fmt.Sprintf("Bicep CLI version %s (abcdef0123)", bicep.Version.String()),
		Stderr: "",
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(args.Cmd, "bicep") && args.Args[0] == "build"
	}).Respond(exec.RunResult{
		Stdout: string(bicepBytes),
		Stderr: "",
	})
}

var testEnvDeployment armresources.DeploymentExtended = armresources.DeploymentExtended{
	ID:       to.Ptr("DEPLOYMENT_ID"),
	Name:     to.Ptr("test-env"),
	Location: to.Ptr("eastus2"),
	Tags: map[string]*string{
		"azd-env-name": to.Ptr("test-env"),
	},
	Type: to.Ptr("Microsoft.Resources/deployments"),
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

func prepareStateMocks(mockContext *mocks.MockContext) {
	deployResultBytes, _ := json.Marshal(testEnvDeployment)

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
			&testEnvDeployment,
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
	makeItem := func(resourceType azapi.AzureResourceType, resourceName string) *armresources.GenericResourceExpanded {
		id := fmt.Sprintf("/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP/%s/%s",
			string(resourceType), resourceName)

		return &armresources.GenericResourceExpanded{
			ID:       to.Ptr(id),
			Name:     to.Ptr(resourceName),
			Type:     to.Ptr(string(resourceType)),
			Location: to.Ptr("eastus2"),
		}
	}

	resourceList := armresources.ResourceListResult{
		Value: []*armresources.GenericResourceExpanded{
			makeItem(azapi.AzureResourceTypeWebSite, "app-123"),
			makeItem(azapi.AzureResourceTypeKeyVault, "kv-123"),
			makeItem(azapi.AzureResourceTypeKeyVault, "kv2-123"),
			makeItem(azapi.AzureResourceTypeManagedHSM, "hsm-123"),
			makeItem(azapi.AzureResourceTypeManagedHSM, "hsm2-123"),
			makeItem(azapi.AzureResourceTypeAppConfig, "ac-123"),
			makeItem(azapi.AzureResourceTypeAppConfig, "ac2-123"),
			makeItem(azapi.AzureResourceTypeApim, "apim-123"),
			makeItem(azapi.AzureResourceTypeApim, "apim2-123"),
		},
	}

	resourceGroup := &armresources.ResourceGroup{
		ID:       to.Ptr(azure.ResourceGroupRID("SUBSCRIPTION_ID", "RESOURCE_GROUP")),
		Location: to.Ptr("eastus2"),
		Name:     to.Ptr("RESOURCE_GROUP"),
		Type:     to.Ptr(string(azapi.AzureResourceTypeResourceGroup)),
		Tags: map[string]*string{
			"azd-env-name": to.Ptr("test-env"),
		},
	}

	// Get resource group
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasSuffix(request.URL.Path, "/resourcegroups") && strings.Contains(request.URL.RawQuery, "filter=")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		result := armresources.ResourceGroupListResult{
			Value: []*armresources.ResourceGroup{
				resourceGroup,
			},
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, result)
	})

	// Get list of resources to delete
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/resources")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, resourceList)
	})

	// Get Key Vault
	getKeyVaultMock(mockContext, "/vaults/kv-123", "kv-123", "eastus2")
	getKeyVaultMock(mockContext, "/vaults/kv2-123", "kv2-123", "eastus2")

	// Get Managed HSM
	getManagedHSMMock(mockContext, "/managedHSMs/hsm-123", "hsm-123", "eastus2")
	getManagedHSMMock(mockContext, "/managedHSMs/hsm2-123", "hsm2-123", "eastus2")

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

	// Purge Key Vault
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost &&
			(strings.HasSuffix(request.URL.Path, "deletedVaults/kv-123/purge") ||
				strings.HasSuffix(request.URL.Path, "deletedVaults/kv2-123/purge"))
	}).RespondFn(httpRespondFn)

	// Set up the end of any LRO with a 204 response since we are using the Location header.
	mockPollingUrl := "https://url-to-poll.net/keep-deleting"
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(
				request.URL.String(), mockPollingUrl)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(request, 204)
	})

	// Purge Managed HSM
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost &&
			(strings.HasSuffix(request.URL.Path, "deletedManagedHSMs/hsm-123/purge") ||
				strings.HasSuffix(request.URL.Path, "deletedManagedHSMs/hsm2-123/purge"))
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		response, err := mocks.CreateEmptyHttpResponse(request, http.StatusAccepted)
		response.Header.Add("location", mockPollingUrl)
		return response, err
	})

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
		return request.Method == http.MethodPut &&
			strings.Contains(request.URL.Path, "/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		result := &armresources.DeploymentsClientCreateOrUpdateAtSubscriptionScopeResponse{
			DeploymentExtended: armresources.DeploymentExtended{
				ID:       to.Ptr("DEPLOYMENT_ID"),
				Name:     to.Ptr("test-env"),
				Location: to.Ptr("eastus2"),
				Tags: map[string]*string{
					"azd-env-name": to.Ptr("test-env"),
				},
				Type: to.Ptr("Microsoft.Resources/deployments"),
				Properties: &armresources.DeploymentPropertiesExtended{
					ProvisioningState: to.Ptr(armresources.ProvisioningStateSucceeded),
					Timestamp:         to.Ptr(time.Now()),
				},
			},
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, result)
	})
}

func getKeyVaultMock(mockContext *mocks.MockContext, keyVaultString string, name string, location string) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, keyVaultString)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		keyVaultResponse := armkeyvault.VaultsClientGetResponse{
			Vault: armkeyvault.Vault{
				ID: to.Ptr(
					fmt.Sprintf("/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP/%s/%s",
						string(azapi.AzureResourceTypeKeyVault), name)),
				Name:     to.Ptr(name),
				Location: to.Ptr(location),
				Properties: &armkeyvault.VaultProperties{
					EnableSoftDelete:      to.Ptr(true),
					EnablePurgeProtection: to.Ptr(false),
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

func getManagedHSMMock(mockContext *mocks.MockContext, managedHSMString string, name string, location string) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, managedHSMString)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		managedHSMResponse := armkeyvault.ManagedHsmsClientGetResponse{
			ManagedHsm: armkeyvault.ManagedHsm{
				ID: to.Ptr(
					fmt.Sprintf("/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP/%s/%s",
						string(azapi.AzureResourceTypeManagedHSM), name)),
				Name:     to.Ptr(name),
				Location: to.Ptr(location),
				Properties: &armkeyvault.ManagedHsmProperties{
					EnableSoftDelete:      to.Ptr(true),
					EnablePurgeProtection: to.Ptr(false),
				},
			},
		}

		managedHSMBytes, _ := json.Marshal(managedHSMResponse)

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer(managedHSMBytes)),
		}, nil
	})
}

func getAppConfigMock(mockContext *mocks.MockContext, appConfigString string, name string, location string) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, appConfigString)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		appConfigResponse := armappconfiguration.ConfigurationStoresClientGetResponse{
			ConfigurationStore: armappconfiguration.ConfigurationStore{
				ID: to.Ptr(
					fmt.Sprintf("/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP/%s/%s",
						string(azapi.AzureResourceTypeAppConfig), name)),

				Name:     to.Ptr(name),
				Location: to.Ptr(location),
				Properties: &armappconfiguration.ConfigurationStoreProperties{
					EnablePurgeProtection: to.Ptr(false),
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
				ID: to.Ptr(
					fmt.Sprintf("/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP/%s/%s",
						string(azapi.AzureResourceTypeApim), name)),

				Name:     to.Ptr(name),
				Location: to.Ptr(location),
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

func TestResourceGroupsFromDeployment(t *testing.T) {
	t.Parallel()

	t.Run("references used when no output resources", func(t *testing.T) {
		mockDeployment := &azapi.ResourceDeployment{
			//nolint:lll
			Id: "/subscriptions/faa080af-c1d8-40ad-9cce-e1a450ca5b57/providers/Microsoft.Resources/deployments/matell-2508-1689982746",
			//nolint:lll
			DeploymentId: "/subscriptions/faa080af-c1d8-40ad-9cce-e1a450ca5b57/providers/Microsoft.Resources/deployments/matell-2508-1689982746",
			Name:         "matell-2508",
			Type:         "Microsoft.Resources/deployments",
			Tags: map[string]*string{
				"azd-env-name": to.Ptr("matell-2508"),
			},
			ProvisioningState: azapi.DeploymentProvisioningStateFailed,
			Timestamp:         time.Now(),
			Dependencies: []*armresources.Dependency{
				{
					//nolint:lll
					ID: to.Ptr(
						"/subscriptions/faa080af-c1d8-40ad-9cce-e1a450ca5b57/resourceGroups/matell-2508-rg/providers/Microsoft.Resources/deployments/resources",
					),
					ResourceName: to.Ptr("resources"),
					ResourceType: to.Ptr("Microsoft.Resources/deployments"),
					DependsOn: []*armresources.BasicDependency{
						{
							//nolint:lll
							ID: to.Ptr(
								"/subscriptions/faa080af-c1d8-40ad-9cce-e1a450ca5b57/resourceGroups/matell-2508-rg",
							),
							ResourceName: to.Ptr("matell-2508-rg"),
							ResourceType: to.Ptr("Microsoft.Resources/resourceGroups"),
						},
					},
				},
			},
		}

		require.Equal(t, []string{"matell-2508-rg"}, resourceGroupsToDelete(mockDeployment))
	})

	t.Run("duplicate resource groups ignored", func(t *testing.T) {

		mockDeployment := azapi.ResourceDeployment{
			Id:   "DEPLOYMENT_ID",
			Name: "test-env",
			Resources: []*armresources.ResourceReference{
				{
					ID: to.Ptr("/subscriptions/sub-id/resourceGroups/groupA"),
				},
				{
					ID: to.Ptr(
						"/subscriptions/sub-id/resourceGroups/groupA/Microsoft.Storage/storageAccounts/storageAccount",
					),
				},
				{
					ID: to.Ptr("/subscriptions/sub-id/resourceGroups/groupB"),
				},
				{
					ID: to.Ptr("/subscriptions/sub-id/resourceGroups/groupB/Microsoft.web/sites/test"),
				},
				{
					ID: to.Ptr("/subscriptions/sub-id/resourceGroups/groupC"),
				},
			},
			ProvisioningState: azapi.DeploymentProvisioningStateSucceeded,
			Timestamp:         time.Now(),
		}

		groups := resourceGroupsToDelete(&mockDeployment)

		sort.Strings(groups)
		require.Equal(t, []string{"groupA", "groupB", "groupC"}, groups)
	})
}

// From a mocked list of deployments where there are multiple deployments with the matching tag, expect to pick the most
// recent one.
func TestFindCompletedDeployments(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(args.Cmd, "bicep") && strings.Contains(command, "--version")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, fmt.Sprintf("Bicep CLI version %s (abcdef0123)", bicep.Version), ""), nil
	})
	// Have `bicep build` return a ARM template that targets a resource group.
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(args.Cmd, "bicep") && args.Args[0] == "build"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
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

		return exec.RunResult{
			Stdout: string(bicepBytes),
		}, nil
	})

	bicepProvider := createBicepProvider(t, mockContext)

	baseDate := "1989-10-31"
	envTag := "env-tag"

	deployments, err := bicepProvider.deploymentManager.CompletedDeployments(
		*mockContext.Context, &mockedScope{
			baseDate: baseDate,
			envTag:   envTag,
		}, envTag, "")
	require.NoError(t, err)
	require.Equal(t, 1, len(deployments))
	// should take the base date + 2 years
	expectedDate, err := time.Parse(time.DateOnly, baseDate)
	require.NoError(t, err)
	expectedDate = expectedDate.Add(time.Hour * 24 * 365 * 2)

	deploymentDate := deployments[0].Timestamp
	require.Equal(t, expectedDate, deploymentDate)
}

type mockedScope struct {
	envTag   string
	baseDate string
}

func (m *mockedScope) SubscriptionId() string {
	return "sub-id"
}

func (m *mockedScope) Deployment(deploymentName string) infra.Deployment {
	return &infra.SubscriptionDeployment{}
}

// Return 3 deployments with the expected tag with one year difference each
func (m *mockedScope) ListDeployments(ctx context.Context) ([]*azapi.ResourceDeployment, error) {
	tags := map[string]*string{
		azure.TagKeyAzdEnvName: &m.envTag,
	}
	baseDate, err := time.Parse(time.DateOnly, m.baseDate)
	if err != nil {
		return nil, err
	}
	// add one year
	secondDate := baseDate.Add(time.Hour * 24 * 365)
	thirdDate := secondDate.Add(time.Hour * 24 * 365)

	return []*azapi.ResourceDeployment{
		{
			Tags:              tags,
			ProvisioningState: azapi.DeploymentProvisioningStateSucceeded,
			Timestamp:         baseDate,
		},
		{
			Tags:              tags,
			ProvisioningState: azapi.DeploymentProvisioningStateSucceeded,
			Timestamp:         secondDate,
		},
		{
			Tags:              tags,
			ProvisioningState: azapi.DeploymentProvisioningStateSucceeded,
			Timestamp:         thirdDate,
		},
	}, nil
}

func TestUserDefinedTypes(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(args.Cmd, "bicep") && args.Args[0] == "--version"
	}).Respond(exec.RunResult{
		Stdout: fmt.Sprintf("Bicep CLI version %s (abcdef0123)", bicep.Version.String()),
		Stderr: "",
	})

	azCli := mockazapi.NewAzureClientFromMockContext(mockContext)
	bicepCli, err := bicep.NewCli(*mockContext.Context, mockContext.Console, mockContext.CommandRunner)
	require.NoError(t, err)
	env := environment.NewWithValues("test-env", map[string]string{})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(args.Cmd, "bicep") && args.Args[0] == "build"
	}).Respond(exec.RunResult{
		Stdout: userDefinedParamsSample,
		Stderr: "",
	})

	// super basic provider to mock the compileBicep method
	provider := NewBicepProvider(
		azCli,
		bicepCli,
		nil,
		nil,
		&mockenv.MockEnvManager{},
		env,
		mockContext.Console,
		prompt.NewDefaultPrompter(env, mockContext.Console, nil, nil, cloud.AzurePublic()),
		&mockCurrentPrincipal{},
		keyvault.NewKeyVaultService(
			mockaccount.SubscriptionCredentialProviderFunc(
				func(_ context.Context, _ string) (azcore.TokenCredential, error) {
					return mockContext.Credentials, nil
				}),
			mockContext.ArmClientOptions,
			mockContext.CoreClientOptions,
		),
		cloud.AzurePublic(),
	)
	bicepProvider, gooCast := provider.(*BicepProvider)
	require.True(t, gooCast)

	compiled, err := bicepProvider.compileBicep(*mockContext.Context, "user-defined-types")

	require.NoError(t, err)
	require.NotNil(t, compiled)

	template := compiled.Template

	stringParam, exists := template.Parameters["stringParam"]
	require.True(t, exists)
	require.Equal(t, "string", stringParam.Type)
	require.Equal(t, "foo", stringParam.DefaultValue)
	require.Nil(t, stringParam.AllowedValues)

	stringLimitedParam, exists := template.Parameters["stringLimitedParam"]
	require.True(t, exists)
	require.Equal(t, "string", stringLimitedParam.Type)
	require.NotNil(t, stringLimitedParam.AllowedValues)
	require.Equal(t, []interface{}{"arm", "azure", "bicep"}, *stringLimitedParam.AllowedValues)

	intType, exists := template.Parameters["intType"]
	require.True(t, exists)
	require.Equal(t, "int", intType.Type)
	require.NotNil(t, intType.AllowedValues)
	require.Equal(t, []interface{}{float64(10)}, *intType.AllowedValues)

	boolParam, exists := template.Parameters["boolParam"]
	require.True(t, exists)
	require.Equal(t, "bool", boolParam.Type)
	require.NotNil(t, boolParam.AllowedValues)
	require.Equal(t, []interface{}{true}, *boolParam.AllowedValues)

	arrayStringType, exists := template.Parameters["arrayParam"]
	require.True(t, exists)
	require.Equal(t, "array", arrayStringType.Type)
	require.Nil(t, arrayStringType.AllowedValues)

	arrayLimitedParam, exists := template.Parameters["arrayLimitedParam"]
	require.True(t, exists)
	require.Equal(t, "array", arrayLimitedParam.Type)
	require.NotNil(t, arrayLimitedParam.AllowedValues)
	require.Equal(t, []interface{}{"a", "b", "c"}, *arrayLimitedParam.AllowedValues)

	mixedParam, exists := template.Parameters["mixedParam"]
	require.True(t, exists)
	require.Equal(t, "array", mixedParam.Type)
	require.NotNil(t, mixedParam.AllowedValues)
	require.Equal(
		t, []interface{}{"fizz", float64(42), nil, map[string]interface{}{"an": "object"}}, *mixedParam.AllowedValues)

	objectParam, exists := template.Parameters["objectParam"]
	require.True(t, exists)
	require.Equal(t, "object", objectParam.Type)
	require.Nil(t, objectParam.AllowedValues)
	require.NotNil(t, objectParam.Properties)
	require.Equal(
		t,
		azure.ArmTemplateParameterDefinitions{
			"name": {Type: "string"},
			"sku":  {Type: "string"},
		},
		objectParam.Properties)
	require.NotNil(t, objectParam.AdditionalProperties)
	require.True(t, objectParam.AdditionalProperties.HasAdditionalProperties())
	require.Equal(
		t,
		azure.ArmTemplateParameterAdditionalPropertiesProperties{
			Type:      "string",
			MinLength: to.Ptr(10),
			Metadata: map[string]json.RawMessage{
				"fromDefinitionFoo": []byte(`"foo"`),
				"fromDefinitionBar": []byte(`"bar"`),
			},
		},
		objectParam.AdditionalProperties.Properties())
	require.NotNil(t, objectParam.Metadata)
	require.Equal(
		t,
		map[string]json.RawMessage{
			// Note: Validating the metadata combining and override here.
			// The parameter definition contains metadata that is automatically added to the parameter.
			// Then the parameter also has metadata and overrides one of the values from the definition.
			"fromDefinitionFoo": []byte(`"foo"`),
			"fromDefinitionBar": []byte(`"override"`),
			"fromParameter":     []byte(`"parameter"`),
		},
		objectParam.Metadata)

	sealedObjectParam, exists := template.Parameters["sealedObjectParam"]
	require.True(t, exists)
	require.Equal(t, "object", sealedObjectParam.Type)
	require.Nil(t, sealedObjectParam.AllowedValues)
	require.NotNil(t, sealedObjectParam.Properties)
	require.Equal(
		t,
		azure.ArmTemplateParameterDefinitions{
			"name": {Type: "string"},
			"sku":  {Type: "string"},
		},
		sealedObjectParam.Properties)
	require.NotNil(t, sealedObjectParam.AdditionalProperties)
	require.False(t, sealedObjectParam.AdditionalProperties.HasAdditionalProperties())

	// output resolves just the type. Value and Metadata should persist
	customOutput, exists := template.Outputs["customOutput"]
	require.True(t, exists)
	require.Equal(t, "string", customOutput.Type)
	require.Equal(t, "[parameters('stringLimitedParam')]", customOutput.Value)
	require.Equal(t, map[string]interface{}{
		"foo": "bar",
	}, customOutput.Metadata)
}

func Test_armParameterFileValue(t *testing.T) {
	t.Run("NilValue", func(t *testing.T) {
		actual := armParameterFileValue(provisioning.ParameterTypeString, nil, nil)
		require.Nil(t, actual)
	})

	t.Run("StringWithValue", func(t *testing.T) {
		expected := "value"
		actual := armParameterFileValue(provisioning.ParameterTypeString, expected, nil)
		require.Equal(t, expected, actual)
	})

	t.Run("EmptyString", func(t *testing.T) {
		input := ""
		actual := armParameterFileValue(provisioning.ParameterTypeString, input, nil)
		require.Nil(t, actual)
	})

	t.Run("EmptyStringWithNonEmptyDefault", func(t *testing.T) {
		expected := ""
		actual := armParameterFileValue(provisioning.ParameterTypeString, expected, "not-empty")
		require.Equal(t, expected, actual)
	})

	t.Run("EmptyStringWithEmptyDefault", func(t *testing.T) {
		input := ""
		actual := armParameterFileValue(provisioning.ParameterTypeString, input, "")
		require.Nil(t, actual)
	})

	t.Run("ValidBool", func(t *testing.T) {
		expected := true
		actual := armParameterFileValue(provisioning.ParameterTypeBoolean, expected, nil)
		require.Equal(t, expected, actual)
	})

	t.Run("ActualBool", func(t *testing.T) {
		expected := true
		actual := armParameterFileValue(provisioning.ParameterTypeBoolean, "true", nil)
		require.Equal(t, expected, actual)
	})

	t.Run("InvalidBool", func(t *testing.T) {
		actual := armParameterFileValue(provisioning.ParameterTypeBoolean, "NotABool", nil)
		require.Nil(t, actual)
	})

	t.Run("ValidInt", func(t *testing.T) {
		var expected int64 = 42
		actual := armParameterFileValue(provisioning.ParameterTypeNumber, "42", nil)
		require.Equal(t, expected, actual)
	})

	t.Run("ActualInt", func(t *testing.T) {
		var expected int64 = 42
		actual := armParameterFileValue(provisioning.ParameterTypeNumber, expected, nil)
		require.Equal(t, expected, actual)
	})

	t.Run("InvalidInt", func(t *testing.T) {
		actual := armParameterFileValue(provisioning.ParameterTypeNumber, "NotAnInt", nil)
		require.Nil(t, actual)
	})

	t.Run("Array", func(t *testing.T) {
		expected := []string{"a", "b", "c"}
		actual := armParameterFileValue(provisioning.ParameterTypeArray, expected, nil)
		require.Equal(t, expected, actual)
	})
}

const userDefinedParamsSample = `{
	"$schema": "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
	"languageVersion": "2.0",
	"definitions": {
	  "stringType": {
		"type": "string"
	  },
	  "stringLimitedType": {
		"type": "string",
		"allowedValues": [
		  "arm",
		  "azure",
		  "bicep"
		]
	  },
	  "intType": {
		"type": "int",
		"allowedValues": [
		  10
		]
	  },
	  "boolType": {
		"type": "bool",
		"allowedValues": [
		  true
		]
	  },
	  "arrayStringType": {
		"type": "array",
		"items": {
		  "type": "string"
		}
	  },
	  "arrayStringLimitedType": {
		"type": "array",
		"allowedValues": [
		  "a",
		  "b",
		  "c"
		]
	  },
	  "mixedType": {
		"type": "array",
		"allowedValues": [
		  "fizz",
		  42,
		  null,
		  {
			"an": "object"
		  }
		]
	  },
	  "objectType": {
		"type": "object",
		"properties": {
		  "name": {
			"type": "string"
		  },
		  "sku": {
			"type": "string"
		  }
		},
		"additionalProperties": {
			"type": "string",
			"minLength": 10,
			"metadata": {
			  "fromDefinitionFoo": "foo",
			  "fromDefinitionBar": "bar"
			}
		},
		"metadata": {
			"fromDefinitionFoo": "foo",
			"fromDefinitionBar": "bar"
		}
	  },
	  "sealedObjectType": {
		"type": "object",
		"properties": {
		  "name": {
			"type": "string"
		  },
		  "sku": {
			"type": "string"
		  }
		},
		"additionalProperties": false
	  }
	},
	"parameters": {
	  "stringParam": {
		"$ref": "#/definitions/stringType",
		"defaultValue": "foo"
	  },
	  "stringLimitedParam": {
		"$ref": "#/definitions/stringLimitedType"
	  },
	  "intType": {
		"$ref": "#/definitions/intType"
	  },
	  "boolParam": {
		"$ref": "#/definitions/boolType"
	  },
	  "arrayParam": {
		"$ref": "#/definitions/arrayStringType"
	  },
	  "arrayLimitedParam": {
		"$ref": "#/definitions/arrayStringLimitedType"
	  },
	  "mixedParam": {
		"$ref": "#/definitions/mixedType"
	  },
	  "objectParam": {
		"$ref": "#/definitions/objectType",
		"metadata": {
			"fromDefinitionBar": "override",
			"fromParameter": "parameter"
		  }
	  },
	  "sealedObjectParam": {
		"$ref": "#/definitions/sealedObjectType"
	  }
	},
	"resources": {},
	"outputs": {
		"customOutput": {
			"$ref": "#/definitions/stringLimitedType",
			"metadata": {
				"foo": "bar"
			},
			"value": "[parameters('stringLimitedParam')]"
		}
	}
}`

func TestInputsParameter(t *testing.T) {
	existingInputs := map[string]map[string]interface{}{
		"resource1": {
			"input1": "value1",
		},
		"resource2": {
			"input2": "value2",
		},
	}

	autoGenParameters := map[string]map[string]azure.AutoGenInput{
		"resource1": {
			"input1": {
				Length: 10,
			},
			"input3": {
				Length: 8,
			},
		},
		"resource2": {
			"input2": {
				Length: 12,
			},
		},
		"resource3": {
			"input4": {
				Length: 6,
			},
		},
	}

	expectedInputsParameter := map[string]map[string]interface{}{
		"resource1": {
			"input1": "value1",
			"input3": "to-be-gen-with-len-8",
		},
		"resource2": {
			"input2": "value2",
		},
		"resource3": {
			"input4": "to-be-gen-with-len-6",
		},
	}

	expectedInputsUpdated := true

	inputsParameter, inputsUpdated, err := inputsParameter(existingInputs, autoGenParameters)

	require.NoError(t, err)
	result, parse := inputsParameter.Value.(map[string]map[string]interface{})
	require.True(t, parse)

	require.Equal(
		t, expectedInputsParameter["resource1"]["input1"], result["resource1"]["input1"])
	// generated - only check length
	require.Equal(
		t, autoGenParameters["resource1"]["input3"].Length, uint(len(result["resource1"]["input3"].(string))))

	require.Equal(t, expectedInputsParameter["resource2"], result["resource2"])

	// generated - only check length
	require.Equal(
		t, autoGenParameters["resource3"]["input4"].Length, uint(len(result["resource3"]["input4"].(string))))

	require.Equal(t, expectedInputsUpdated, inputsUpdated)
}
