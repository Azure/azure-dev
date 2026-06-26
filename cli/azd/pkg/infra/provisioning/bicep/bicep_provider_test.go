// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/apimanagement/armapimanagement"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appconfiguration/armappconfiguration"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
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
	"github.com/azure/azure-dev/cli/azd/test/mocks/mocktracing"
)

func TestBicepPlan(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	prepareBicepMocks(mockContext)
	infraProvider := createBicepProvider(t, mockContext)

	deploymentPlan, err := infraProvider.plan(*mockContext.Context)

	require.Nil(t, err)

	configuredParameters := deploymentPlan.Parameters

	require.Equal(t, infraProvider.env.GetLocation(), configuredParameters["location"].Value)
	require.Equal(
		t,
		infraProvider.env.Name(),
		configuredParameters["environmentName"].Value,
	)
}

func TestBicepPlanKeyVaultRef(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	prepareBicepMocks(mockContext)
	infraProvider := createBicepProvider(t, mockContext)

	deploymentPlan, err := infraProvider.plan(*mockContext.Context)

	require.Nil(t, err)

	configuredParameters := deploymentPlan.Parameters

	require.NotEmpty(t, configuredParameters["kvSecret"])
	require.NotNil(t, configuredParameters["kvSecret"].KeyVaultReference)
	require.Nil(t, configuredParameters["kvSecret"].Value)
	require.Equal(t, "secretName", configuredParameters["kvSecret"].KeyVaultReference.SecretName)
}

func TestBicepPlanParameterTypes(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	prepareBicepMocks(mockContext)
	infraProvider := createBicepProvider(t, mockContext)

	deploymentPlan, err := infraProvider.plan(*mockContext.Context)

	require.Nil(t, err)

	configuredParameters := deploymentPlan.Parameters

	require.NotEmpty(t, configuredParameters["regularString"])
	require.Equal(t, configuredParameters["regularString"].Value, "test")
	require.Empty(t, configuredParameters["emptyString"])
	require.Nil(t, configuredParameters["emptyString"].Value)

	require.NotEmpty(t, configuredParameters["regularObject"])
	require.Equal(t, configuredParameters["regularObject"].Value, map[string]any{"test": "test"})
	require.Equal(t, configuredParameters["emptyObject"].Value, map[string]any{})

	require.NotEmpty(t, configuredParameters["regularArray"])
	require.Equal(t, configuredParameters["regularArray"].Value, []any{"test"})
	require.NotEmpty(t, configuredParameters["emptyArray"])
	require.Equal(t, configuredParameters["emptyArray"].Value, []any{})
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
	mockContext := mocks.NewMockContext(t.Context())

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

	require.Equal(t, "value", plan.Parameters["stringParam"].Value)
}

func TestBicepState(t *testing.T) {
	expectedWebsiteUrl := "http://myapp.azurewebsites.net"

	mockContext := mocks.NewMockContext(t.Context())
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
		mockContext := mocks.NewMockContext(t.Context())
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
		mockContext := mocks.NewMockContext(t.Context())
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

func TestBicepDestroyLogAnalyticsWorkspace(t *testing.T) {
	t.Run("WithPurge", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		prepareBicepMocks(mockContext)
		prepareStateMocks(mockContext)
		prepareLogAnalyticsDestroyMocks(mockContext)

		infraProvider := createBicepProvider(t, mockContext)

		destroyOptions := provisioning.NewDestroyOptions(true, true)
		destroyResult, err := infraProvider.Destroy(*mockContext.Context, destroyOptions)

		require.NoError(t, err)
		require.NotNil(t, destroyResult)

		consoleOutput := mockContext.Console.Output()
		require.Len(t, consoleOutput, 2)
		require.Contains(t, consoleOutput[0], "Deleting your resources can take some time")
		require.Contains(t, consoleOutput[1], "")
	})
}

func TestWarnExternalResourceGroup(t *testing.T) {
	setupProvider := func(t *testing.T, mockContext *mocks.MockContext) *BicepProvider {
		t.Helper()
		env := environment.NewWithValues("test-env", map[string]string{
			environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
		})
		resourceService := azapi.NewResourceService(
			mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions,
		)
		return &BicepProvider{
			env:             env,
			console:         mockContext.Console,
			resourceService: resourceService,
		}
	}

	mockRGResponse := func(
		mockContext *mocks.MockContext, rgName string, tags map[string]*string,
	) {
		mockContext.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodGet &&
				strings.Contains(req.URL.Path, "/resourcegroups/"+rgName)
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
				armresources.ResourceGroup{
					ID:       new("/subscriptions/SUBSCRIPTION_ID/resourceGroups/" + rgName),
					Name:     new(rgName),
					Location: new("eastus"),
					Tags:     tags,
				})
		})
	}

	t.Run("SkipsWhenForced", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		provider := setupProvider(t, mockContext)
		options := provisioning.NewDestroyOptions(true, false)

		confirmed, err := provider.warnExternalResourceGroup(
			*mockContext.Context, options, "my-rg", nil, 0,
		)
		require.NoError(t, err)
		assert.True(t, confirmed)
	})

	t.Run("NoWarningWhenRGHasMatchingTag", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		mockRGResponse(mockContext, "my-rg", map[string]*string{
			"azd-env-name": new("test-env"),
		})
		provider := setupProvider(t, mockContext)
		options := provisioning.NewDestroyOptions(false, false)

		confirmed, err := provider.warnExternalResourceGroup(
			*mockContext.Context, options, "my-rg", nil, 0,
		)
		require.NoError(t, err)
		assert.False(t, confirmed)
	})

	t.Run("WarnsWhenRGHasMismatchedTag", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		mockRGResponse(mockContext, "my-rg", map[string]*string{
			"azd-env-name": new("other-env"),
		})
		provider := setupProvider(t, mockContext)
		options := provisioning.NewDestroyOptions(false, false)

		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return true
		}).Respond(false)

		_, err := provider.warnExternalResourceGroup(
			*mockContext.Context, options, "my-rg", nil, 0,
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "user denied delete confirmation")
	})

	t.Run("WarnsAndDeniesWhenRGHasNoTag", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		mockRGResponse(mockContext, "my-rg", nil)
		provider := setupProvider(t, mockContext)
		options := provisioning.NewDestroyOptions(false, false)

		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			// Verify DefaultValue is false — this is what --no-prompt uses to deny by default
			assert.False(t, options.DefaultValue.(bool))
			return true
		}).Respond(false)

		_, err := provider.warnExternalResourceGroup(
			*mockContext.Context, options, "my-rg", nil, 0,
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "user denied delete confirmation")
	})

	t.Run("ProceedsWhenUserConfirms", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		mockRGResponse(mockContext, "my-rg", nil)
		provider := setupProvider(t, mockContext)
		options := provisioning.NewDestroyOptions(false, false)

		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return true
		}).Respond(true)

		confirmed, err := provider.warnExternalResourceGroup(
			*mockContext.Context, options, "my-rg", nil, 0,
		)
		require.NoError(t, err)
		assert.True(t, confirmed)
	})

	t.Run("FailsClosedWhenGetRGErrors", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		mockContext.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodGet &&
				strings.Contains(req.URL.Path, "/resourcegroups/my-rg")
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateEmptyHttpResponse(req, http.StatusInternalServerError)
		})
		provider := setupProvider(t, mockContext)
		options := provisioning.NewDestroyOptions(false, false)

		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return true
		}).Respond(false)

		_, err := provider.warnExternalResourceGroup(
			*mockContext.Context, options, "my-rg", nil, 0,
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "user denied delete confirmation")
	})
}

func TestDeploymentForResourceGroup(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

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
					ID:       new("/subscriptions/SUBSCRIPTION_ID/resourcegroups/existingGroup2"),
					Name:     new("existingGroup2"),
					Type:     new("Microsoft.Resources/resourceGroup"),
					Location: new("eastus2"),
				},
				{
					ID:       new("/subscriptions/SUBSCRIPTION_ID/resourcegroups/existingGroup1"),
					Name:     new("existingGroup1"),
					Type:     new("Microsoft.Resources/resourceGroup"),
					Location: new("eastus2"),
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
			ID:       new("/subscriptions/SUBSCRIPTION_ID/resourcegroups/rg-test-env"),
			Name:     new("rg-test-env"),
			Type:     new("Microsoft.Resources/resourceGroup"),
			Location: new("eastus2"),
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
		require.Equal(t, "1. Create a new resource group", options.Options[0])
		require.Equal(t, "2. existingGroup1", options.Options[1])
		require.Equal(t, "3. existingGroup2", options.Options[2])

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

	deployment, err := infraProvider.generateDeploymentObject(planResult)
	require.NoError(t, err)
	require.Equal(t, "rg-test-env",
		deployment.(*infra.ResourceGroupDeployment).ResourceGroupName())
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
	projectDir := "../../../../test/functional/testdata/mock-samples/webapp"
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
	envManager.On("Reload", mock.Anything, mock.Anything).Return(nil)

	bicepCli := bicep.NewCli(mockContext.Console, mockContext.CommandRunner)
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
		&mockResourceManager{},
		deploymentManager,
		envManager,
		env,
		mockContext.Console,
		prompt.NewDefaultPrompter(env, mockContext.Console, accountManager, nil, resourceService, cloud.AzurePublic()),
		&mockCurrentPrincipal{},
		keyvault.NewKeyVaultService(
			mockaccount.SubscriptionCredentialProviderFunc(
				func(_ context.Context, _ string) (azcore.TokenCredential, error) {
					return mockContext.Credentials, nil
				}),
			mockContext.ArmClientOptions,
			mockContext.CoreClientOptions,
			cloud.AzurePublic(),
		),
		cloud.AzurePublic(),
		nil,
		nil,
		mockContext.Container,
	)

	err := provider.Initialize(*mockContext.Context, projectDir, options)
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
			"kvSecret":        {Type: "securestring"},
			"regularString":   {Type: "string", DefaultValue: ""},
			"emptyString":     {Type: "string", DefaultValue: ""},
			"regularObject":   {Type: "array", DefaultValue: make([]string, 0)},
			"emptyObject":     {Type: "array", DefaultValue: make([]string, 0)},
			"regularArray":    {Type: "object", DefaultValue: make(map[string]int)},
			"emptyArray":      {Type: "object", DefaultValue: make(map[string]int)},
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
	ID:       new("DEPLOYMENT_ID"),
	Name:     new("test-env"),
	Location: new("eastus2"),
	Tags: map[string]*string{
		"azd-env-name": new("test-env"),
	},
	Type: new("Microsoft.Resources/deployments"),
	Properties: &armresources.DeploymentPropertiesExtended{
		Outputs: map[string]any{
			"WEBSITE_URL": map[string]any{"value": "http://myapp.azurewebsites.net", "type": "string"},
		},
		OutputResources: []*armresources.ResourceReference{
			{
				ID: new("/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP"),
			},
		},
		ProvisioningState: to.Ptr(armresources.ProvisioningStateSucceeded),
		Timestamp:         new(time.Now()),
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
			ID:       &id,
			Name:     new(resourceName),
			Type:     new(string(resourceType)),
			Location: new("eastus2"),
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
		ID:       new(azure.ResourceGroupRID("SUBSCRIPTION_ID", "RESOURCE_GROUP")),
		Location: new("eastus2"),
		Name:     new("RESOURCE_GROUP"),
		Type:     to.Ptr(string(azapi.AzureResourceTypeResourceGroup)),
		Tags: map[string]*string{
			"azd-env-name": new("test-env"),
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
				ID:       new("DEPLOYMENT_ID"),
				Name:     new("test-env"),
				Location: new("eastus2"),
				Tags: map[string]*string{
					"azd-env-name": new("test-env"),
				},
				Type: new("Microsoft.Resources/deployments"),
				Properties: &armresources.DeploymentPropertiesExtended{
					ProvisioningState: to.Ptr(armresources.ProvisioningStateSucceeded),
					Timestamp:         new(time.Now()),
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
				ID: new(
					fmt.Sprintf("/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP/%s/%s",
						string(azapi.AzureResourceTypeKeyVault), name)),
				Name:     new(name),
				Location: new(location),
				Properties: &armkeyvault.VaultProperties{
					EnableSoftDelete:      new(true),
					EnablePurgeProtection: new(false),
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
				ID: new(
					fmt.Sprintf("/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP/%s/%s",
						string(azapi.AzureResourceTypeManagedHSM), name)),
				Name:     new(name),
				Location: new(location),
				Properties: &armkeyvault.ManagedHsmProperties{
					EnableSoftDelete:      new(true),
					EnablePurgeProtection: new(false),
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
				ID: new(
					fmt.Sprintf("/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP/%s/%s",
						string(azapi.AzureResourceTypeAppConfig), name)),

				Name:     new(name),
				Location: new(location),
				Properties: &armappconfiguration.ConfigurationStoreProperties{
					EnablePurgeProtection: new(false),
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
				ID: new(
					fmt.Sprintf("/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP/%s/%s",
						string(azapi.AzureResourceTypeApim), name)),

				Name:     new(name),
				Location: new(location),
			},
		}

		apimBytes, _ := json.Marshal(apimResponse)

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer(apimBytes)),
		}, nil
	})
}

func getLogAnalyticsMock(mockContext *mocks.MockContext, logAnalyticsString string, name string) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, logAnalyticsString)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		logAnalyticsResponse := map[string]any{
			"id": fmt.Sprintf("/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP/%s/%s",
				string(azapi.AzureResourceTypeLogAnalyticsWorkspace), name),
			"name": name,
		}

		responseBytes, _ := json.Marshal(logAnalyticsResponse)

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer(responseBytes)),
		}, nil
	})
}

func prepareLogAnalyticsDestroyMocks(mockContext *mocks.MockContext) {
	makeItem := func(resourceType azapi.AzureResourceType, resourceName string) *armresources.GenericResourceExpanded {
		id := fmt.Sprintf("/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP/%s/%s",
			string(resourceType), resourceName)

		return &armresources.GenericResourceExpanded{
			ID:       &id,
			Name:     new(resourceName),
			Type:     new(string(resourceType)),
			Location: new("eastus2"),
		}
	}

	resourceList := armresources.ResourceListResult{
		Value: []*armresources.GenericResourceExpanded{
			makeItem(azapi.AzureResourceTypeLogAnalyticsWorkspace, "la-workspace-123"),
			makeItem(azapi.AzureResourceTypeLogAnalyticsWorkspace, "la-workspace2-123"),
		},
	}

	resourceGroup := &armresources.ResourceGroup{
		ID:       new(azure.ResourceGroupRID("SUBSCRIPTION_ID", "RESOURCE_GROUP")),
		Location: new("eastus2"),
		Name:     new("RESOURCE_GROUP"),
		Type:     to.Ptr(string(azapi.AzureResourceTypeResourceGroup)),
		Tags: map[string]*string{
			"azd-env-name": new("test-env"),
		},
	}

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

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/resources")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, resourceList)
	})

	getLogAnalyticsMock(mockContext, "/workspaces/la-workspace-123", "la-workspace-123")
	getLogAnalyticsMock(mockContext, "/workspaces/la-workspace2-123", "la-workspace2-123")

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodDelete &&
			(strings.HasSuffix(request.URL.Path, "workspaces/la-workspace-123") ||
				strings.HasSuffix(request.URL.Path, "workspaces/la-workspace2-123"))
	}).RespondFn(httpRespondFn)

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodDelete &&
			strings.HasSuffix(request.URL.Path, "subscriptions/SUBSCRIPTION_ID/resourcegroups/RESOURCE_GROUP")
	}).RespondFn(httpRespondFn)

	mockPollingUrl := "https://url-to-poll.net"
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(request.URL.String(), mockPollingUrl)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(request, 204)
	})

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodDelete &&
			strings.HasSuffix(
				request.URL.Path, "/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/test-env")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(request, 204)
	})

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPut &&
			strings.Contains(request.URL.Path, "/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		result := &armresources.DeploymentsClientCreateOrUpdateAtSubscriptionScopeResponse{
			DeploymentExtended: armresources.DeploymentExtended{
				ID:       new("DEPLOYMENT_ID"),
				Name:     new("test-env"),
				Location: new("eastus2"),
				Tags: map[string]*string{
					"azd-env-name": new("test-env"),
				},
				Type: new("Microsoft.Resources/deployments"),
				Properties: &armresources.DeploymentPropertiesExtended{
					ProvisioningState: to.Ptr(armresources.ProvisioningStateSucceeded),
					Timestamp:         new(time.Now()),
				},
			},
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, result)
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

// From a mocked list of deployments where there are multiple deployments with the matching tag, expect to pick the most
// recent one.
func TestFindCompletedDeployments(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
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
	layerName := ""

	deployments, err := bicepProvider.deploymentManager.CompletedDeployments(
		*mockContext.Context, &mockedScope{
			baseDate: baseDate,
			envTag:   envTag,
		}, envTag, layerName, "")
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

type mockResourceManager struct{}

func (m *mockResourceManager) WalkDeploymentOperations(
	ctx context.Context,
	deployment infra.Deployment,
	fn infra.WalkDeploymentOperationFunc,
) error {
	return nil
}

func (m *mockResourceManager) GetResourceTypeDisplayName(
	ctx context.Context,
	subscriptionId string,
	resourceId string,
	resourceType azapi.AzureResourceType,
) (string, error) {
	return azapi.GetResourceTypeDisplayName(resourceType), nil
}

func (m *mockResourceManager) GetResourceGroupsForEnvironment(
	ctx context.Context,
	subscriptionId string,
	envName string,
) ([]*azapi.Resource, error) {
	return nil, nil
}

func (m *mockResourceManager) FindResourceGroupForEnvironment(
	ctx context.Context,
	subscriptionId string,
	envName string,
) (string, error) {
	return "", nil
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
	mockContext := mocks.NewMockContext(t.Context())
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(args.Cmd, "bicep") && args.Args[0] == "--version"
	}).Respond(exec.RunResult{
		Stdout: fmt.Sprintf("Bicep CLI version %s (abcdef0123)", bicep.Version.String()),
		Stderr: "",
	})

	azCli := mockazapi.NewAzureClientFromMockContext(mockContext)
	bicepCli := bicep.NewCli(mockContext.Console, mockContext.CommandRunner)
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
		&mockResourceManager{},
		nil,
		&mockenv.MockEnvManager{},
		env,
		mockContext.Console,
		prompt.NewDefaultPrompter(env, mockContext.Console, nil, nil, nil, cloud.AzurePublic()),
		&mockCurrentPrincipal{},
		keyvault.NewKeyVaultService(
			mockaccount.SubscriptionCredentialProviderFunc(
				func(_ context.Context, _ string) (azcore.TokenCredential, error) {
					return mockContext.Credentials, nil
				}),
			mockContext.ArmClientOptions,
			mockContext.CoreClientOptions,
			cloud.AzurePublic(),
		),
		cloud.AzurePublic(),
		nil,
		nil,
		mockContext.Container,
	)
	bicepProvider, gooCast := provider.(*BicepProvider)
	require.True(t, gooCast)

	compiled, err := bicepProvider.compileBicep(*mockContext.Context)

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
	require.Equal(t, []any{"arm", "azure", "bicep"}, *stringLimitedParam.AllowedValues)

	intType, exists := template.Parameters["intType"]
	require.True(t, exists)
	require.Equal(t, "int", intType.Type)
	require.NotNil(t, intType.AllowedValues)
	require.Equal(t, []any{float64(10)}, *intType.AllowedValues)

	boolParam, exists := template.Parameters["boolParam"]
	require.True(t, exists)
	require.Equal(t, "bool", boolParam.Type)
	require.NotNil(t, boolParam.AllowedValues)
	require.Equal(t, []any{true}, *boolParam.AllowedValues)

	arrayStringType, exists := template.Parameters["arrayParam"]
	require.True(t, exists)
	require.Equal(t, "array", arrayStringType.Type)
	require.Nil(t, arrayStringType.AllowedValues)

	arrayLimitedParam, exists := template.Parameters["arrayLimitedParam"]
	require.True(t, exists)
	require.Equal(t, "array", arrayLimitedParam.Type)
	require.NotNil(t, arrayLimitedParam.AllowedValues)
	require.Equal(t, []any{"a", "b", "c"}, *arrayLimitedParam.AllowedValues)

	mixedParam, exists := template.Parameters["mixedParam"]
	require.True(t, exists)
	require.Equal(t, "array", mixedParam.Type)
	require.NotNil(t, mixedParam.AllowedValues)
	require.Equal(
		t, []any{"fizz", float64(42), nil, map[string]any{"an": "object"}}, *mixedParam.AllowedValues)

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
			MinLength: new(10),
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
	require.Equal(t, map[string]any{
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
	existingInputs := map[string]map[string]any{
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

	expectedInputsParameter := map[string]map[string]any{
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
	result, parse := inputsParameter.Value.(map[string]map[string]any)
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
func TestDefaultLocationToSelectFn(t *testing.T) {
	t.Run("NoAllowedValuesOrMetadata", func(t *testing.T) {
		param := azure.ArmTemplateParameterDefinition{}
		result := defaultPromptValue(param)
		require.Nil(t, result)
	})

	t.Run("AllowedValuesOnly", func(t *testing.T) {
		param := azure.ArmTemplateParameterDefinition{
			AllowedValues: &[]any{"eastus", "westus"},
		}
		result := defaultPromptValue(param)
		require.NotNil(t, result)
		require.Equal(t, "eastus", *result)
	})

	t.Run("MetadataOnly", func(t *testing.T) {
		defaultLocation := "centralus"
		param := azure.ArmTemplateParameterDefinition{
			Metadata: map[string]json.RawMessage{
				"azd": json.RawMessage(`{"type": "location", "default": "centralus"}`),
			},
		}
		result := defaultPromptValue(param)
		require.NotNil(t, result)
		require.Equal(t, defaultLocation, *result)
	})

	t.Run("AllowedValuesAndMetadata", func(t *testing.T) {
		defaultLocation := "centralus"
		param := azure.ArmTemplateParameterDefinition{
			AllowedValues: &[]any{"eastus", "westus"},
			Metadata: map[string]json.RawMessage{
				"azd": json.RawMessage(`{"type": "location", "default": "centralus"}`),
			},
		}
		result := defaultPromptValue(param)
		require.NotNil(t, result)
		require.Equal(t, defaultLocation, *result)
	})

	t.Run("InvalidMetadata", func(t *testing.T) {
		param := azure.ArmTemplateParameterDefinition{
			Metadata: map[string]json.RawMessage{
				"azd": json.RawMessage(`{"type": "location"}`),
			},
		}
		result := defaultPromptValue(param)
		require.Nil(t, result)
	})
}

func TestPreviewWithNilResourceState(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	prepareBicepMocks(mockContext)

	// Setup the WhatIf endpoint mock to return changes with nil After (simulating Delete)
	// and nil Before (simulating Create)
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost &&
			strings.Contains(request.URL.Path, "/providers/Microsoft.Resources/deployments/") &&
			strings.HasSuffix(request.URL.Path, "/whatIf")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		// Return a WhatIfOperationResult with various scenarios
		whatIfResult := armresources.WhatIfOperationResult{
			Status: new("Succeeded"),
			Properties: &armresources.WhatIfOperationProperties{
				Changes: []*armresources.WhatIfChange{
					// Create scenario: Before is nil, After has value
					{
						ChangeType: to.Ptr(armresources.ChangeTypeCreate),
						ResourceID: new("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Web/sites/app1"),
						Before:     nil,
						After: map[string]any{
							"type": "Microsoft.Web/sites",
							"name": "app1",
						},
					},
					// Delete scenario: After is nil, Before has value
					{
						ChangeType: to.Ptr(armresources.ChangeTypeDelete),
						ResourceID: new("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Web/sites/app2"),
						Before: map[string]any{
							"type": "Microsoft.Web/sites",
							"name": "app2",
						},
						After: nil,
					},
					// Modify scenario: Both Before and After have values
					{
						ChangeType: to.Ptr(armresources.ChangeTypeModify),
						ResourceID: new("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Web/sites/app3"),
						Before: map[string]any{
							"type": "Microsoft.Web/sites",
							"name": "app3",
						},
						After: map[string]any{
							"type": "Microsoft.Web/sites",
							"name": "app3",
						},
					},
					// Edge case: Both Before and After are nil (should be skipped)
					{
						ChangeType: to.Ptr(armresources.ChangeTypeUnsupported),
						ResourceID: new("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Unknown/unknown"),
						Before:     nil,
						After:      nil,
					},
				},
			},
		}

		bodyBytes, _ := json.Marshal(whatIfResult)
		return &http.Response{
			Request:    request,
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer(bodyBytes)),
		}, nil
	})

	infraProvider := createBicepProvider(t, mockContext)

	result, err := infraProvider.Preview(*mockContext.Context)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Preview)
	require.NotNil(t, result.Preview.Properties)

	// We expect 3 changes (the edge case with both nil should be skipped)
	changes := result.Preview.Properties.Changes
	require.Len(t, changes, 3)

	// Verify Create change (uses After)
	assert.Equal(t, provisioning.ChangeTypeCreate, changes[0].ChangeType)
	assert.Equal(t, "Microsoft.Web/sites", changes[0].ResourceType)
	assert.Equal(t, "app1", changes[0].Name)

	// Verify Delete change (uses Before since After is nil)
	assert.Equal(t, provisioning.ChangeTypeDelete, changes[1].ChangeType)
	assert.Equal(t, "Microsoft.Web/sites", changes[1].ResourceType)
	assert.Equal(t, "app2", changes[1].Name)

	// Verify Modify change
	assert.Equal(t, provisioning.ChangeTypeModify, changes[2].ChangeType)
	assert.Equal(t, "Microsoft.Web/sites", changes[2].ResourceType)
	assert.Equal(t, "app3", changes[2].Name)
}

func TestArrayParameterViaEnvVarSimple(t *testing.T) {
	// Test that array/object parameters are correctly identified and handled using provisioning types
	env := environment.NewWithValues("test-env", map[string]string{
		"AZURE_ENV_NAME":        "test-env",
		"AZURE_LOCATION":        "westus2",
		"AZURE_SUBSCRIPTION_ID": "SUBSCRIPTION_ID",
	})

	// Test parameter type identification using provisioning.ParameterTypeFromArmType
	require.Equal(t, provisioning.ParameterTypeArray, provisioning.ParameterTypeFromArmType("array"))
	require.Equal(t, provisioning.ParameterTypeArray, provisioning.ParameterTypeFromArmType("Array"))
	require.Equal(t, provisioning.ParameterTypeObject, provisioning.ParameterTypeFromArmType("object"))
	require.Equal(t, provisioning.ParameterTypeObject, provisioning.ParameterTypeFromArmType("Object"))
	require.Equal(t, provisioning.ParameterTypeObject, provisioning.ParameterTypeFromArmType("secureobject"))
	require.Equal(t, provisioning.ParameterTypeString, provisioning.ParameterTypeFromArmType("string"))

	// Test the helper function with array env var
	arrayVar := `["val1","val2"]`

	result, substResult, err := evalParamEnvSubst(
		arrayVar,
		"principal-id",
		"ServicePrincipal",
		"testParam",
		env,
		nil,
	)

	require.Nil(t, err)
	require.Equal(t, arrayVar, result) // Result should be unchanged when no substitution needed
	require.False(t, substResult.hasUnsetEnvVar)
	require.Empty(t, substResult.mappedEnvVars)
}

func createBicepProviderWithEnv(
	t *testing.T, mockContext *mocks.MockContext, armTemplate azure.ArmTemplate, envVars map[string]string) *BicepProvider {
	return createBicepProviderWithEnvAndMode(t, mockContext, armTemplate, envVars, provisioning.ModeDeploy)
}

func createBicepProviderWithEnvAndMode(
	t *testing.T,
	mockContext *mocks.MockContext,
	armTemplate azure.ArmTemplate,
	envVars map[string]string,
	mode provisioning.Mode,
) *BicepProvider {
	t.Helper()

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

	projectDir := "../../../../test/functional/testdata/mock-samples/webapp"
	options := provisioning.Options{
		Path:   "infra",
		Module: "main",
		Mode:   mode,
	}

	baseEnvVars := map[string]string{
		environment.LocationEnvVarName:       "westus2",
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
		environment.EnvNameEnvVarName:        "test-env",
	}
	maps.Copy(baseEnvVars, envVars)

	env := environment.NewWithValues("test-env", baseEnvVars)

	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", mock.Anything, mock.Anything).Return(nil)
	envManager.On("Reload", mock.Anything, mock.Anything).Return(nil)

	bicepCli := bicep.NewCli(mockContext.Console, mockContext.CommandRunner)
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
		&mockResourceManager{},
		deploymentManager,
		envManager,
		env,
		mockContext.Console,
		prompt.NewDefaultPrompter(env, mockContext.Console, accountManager, nil, resourceService, cloud.AzurePublic()),
		&mockCurrentPrincipal{},
		keyvault.NewKeyVaultService(
			mockaccount.SubscriptionCredentialProviderFunc(
				func(_ context.Context, _ string) (azcore.TokenCredential, error) {
					return mockContext.Credentials, nil
				}),
			mockContext.ArmClientOptions,
			mockContext.CoreClientOptions,
			cloud.AzurePublic(),
		),
		cloud.AzurePublic(),
		nil,
		nil,
		mockContext.Container,
	)

	err := provider.Initialize(*mockContext.Context, projectDir, options)
	require.NoError(t, err)

	return provider.(*BicepProvider)
}

func TestObjectParameterEnvSubst(t *testing.T) {
	// Test env var substitution with object-like JSON content
	env := environment.NewWithValues("test-env", map[string]string{
		"MY_OBJECT": `{"key":"value"}`,
	})

	result, substResult, err := evalParamEnvSubst(
		"${MY_OBJECT}",
		"principal-id",
		"ServicePrincipal",
		"testParam",
		env,
		nil,
	)

	require.Nil(t, err)
	require.Equal(t, `{"key":"value"}`, result)
	require.Len(t, substResult.mappedEnvVars, 1)
	require.Equal(t, "MY_OBJECT", substResult.mappedEnvVars[0])
	require.False(t, substResult.hasUnsetEnvVar)
}

func TestParameterTypeFromArmTypeIdentification(t *testing.T) {
	// Test that ParameterTypeFromArmType correctly identifies parameter types
	require.Equal(t, provisioning.ParameterTypeArray, provisioning.ParameterTypeFromArmType("array"))
	require.Equal(t, provisioning.ParameterTypeArray, provisioning.ParameterTypeFromArmType("Array"))
	require.Equal(t, provisioning.ParameterTypeObject, provisioning.ParameterTypeFromArmType("object"))
	require.Equal(t, provisioning.ParameterTypeObject, provisioning.ParameterTypeFromArmType("Object"))
	require.Equal(t, provisioning.ParameterTypeObject, provisioning.ParameterTypeFromArmType("secureobject"))
	require.Equal(t, provisioning.ParameterTypeObject, provisioning.ParameterTypeFromArmType("secureObject"))
	require.Equal(t, provisioning.ParameterTypeString, provisioning.ParameterTypeFromArmType("string"))
	require.Equal(t, provisioning.ParameterTypeString, provisioning.ParameterTypeFromArmType("String"))
	require.Equal(t, provisioning.ParameterTypeNumber, provisioning.ParameterTypeFromArmType("int"))
	require.Equal(t, provisioning.ParameterTypeNumber, provisioning.ParameterTypeFromArmType("Int"))
	require.Equal(t, provisioning.ParameterTypeBoolean, provisioning.ParameterTypeFromArmType("bool"))
	require.Equal(t, provisioning.ParameterTypeBoolean, provisioning.ParameterTypeFromArmType("Bool"))
	require.Equal(t, provisioning.ParameterTypeString, provisioning.ParameterTypeFromArmType("securestring"))
}

func TestUnsetEnvVarForArrayParameter(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	// Create a template with optional array parameter (has a default value)
	armTemplate := azure.ArmTemplate{
		Schema:         "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
		ContentVersion: "1.0.0.0",
		Parameters: azure.ArmTemplateParameterDefinitions{
			"environmentName": {Type: "string", DefaultValue: "test-env"},
			"location":        {Type: "string", DefaultValue: "westus2"},
			"arrayParam":      {Type: "array", DefaultValue: []any{}},
		},
		Outputs: azure.ArmTemplateOutputs{},
	}

	// Don't set MY_ARRAY in environment, so it should be unset
	// The parameter should be omitted from resolved params because the value is empty and env var is unset
	infraProvider := createBicepProviderWithEnv(t, mockContext, armTemplate, map[string]string{})

	deploymentPlan, err := infraProvider.plan(*mockContext.Context)

	require.Nil(t, err)
	configuredParameters := deploymentPlan.Parameters

	// arrayParam should be omitted since env var is unset
	require.Empty(t, configuredParameters["arrayParam"])
}

func TestStructuredArrayWithNestedEnvVars(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	armTemplate := azure.ArmTemplate{
		Schema:         "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
		ContentVersion: "1.0.0.0",
		Parameters: azure.ArmTemplateParameterDefinitions{
			"environmentName": {Type: "string", DefaultValue: "test-env"},
			"location":        {Type: "string", DefaultValue: "westus2"},
			"arrayParam":      {Type: "array", DefaultValue: []any{}},
		},
		Outputs: azure.ArmTemplateOutputs{},
	}

	// Set up environment variables
	infraProvider := createBicepProviderWithEnv(t, mockContext, armTemplate, map[string]string{
		"VAL1": "value1",
		"VAL2": "value2",
	})

	deploymentPlan, err := infraProvider.plan(*mockContext.Context)

	require.Nil(t, err)
	configuredParameters := deploymentPlan.Parameters

	// When arrayParam has a structured value (not a simple env var reference),
	// it should use Path B (existing logic)
	// This test just ensures Path B still works for complex structured arrays
	require.NotNil(t, configuredParameters)
}

func TestHelperEvalParamEnvSubst(t *testing.T) {
	// Test the evalParamEnvSubst helper function
	env := environment.NewWithValues("test-env", map[string]string{
		"VAR1":      "value1",
		"VAR2":      "value2",
		"UNSET_VAR": "", // simulates an unset var
	})

	result, substResult, err := evalParamEnvSubst(
		"${VAR1}-${VAR2}",
		"principal-id",
		"ServicePrincipal",
		"testParam",
		env,
		nil,
	)

	require.Nil(t, err)
	require.Equal(t, "value1-value2", result)
	require.Len(t, substResult.mappedEnvVars, 2)
	require.Contains(t, substResult.mappedEnvVars, "VAR1")
	require.Contains(t, substResult.mappedEnvVars, "VAR2")
	require.False(t, substResult.hasUnsetEnvVar)
}
func TestSetPreflightOutcome_SetsSpanAndUsageAttributes(t *testing.T) {
	span := &mocktracing.Span{}
	provider := &BicepProvider{}

	diagnosticIDs := []string{"role_assignment_missing", "role_assignment_conditional"}
	provider.setPreflightOutcome(span, preflightOutcomeWarningsAccepted, diagnosticIDs)

	// Verify outcome is set on the span directly.
	outcomeAttr := findSpanAttribute(span.Attributes, "validation.preflight.outcome")
	require.NotNil(t, outcomeAttr, "expected outcome attribute on span")
	require.Equal(t, preflightOutcomeWarningsAccepted, outcomeAttr.Value.AsString())

	// Verify usage-level attributes are set for parent command span correlation.
	usageAttrs := tracing.GetUsageAttributes()
	usageOutcome := findSpanAttribute(usageAttrs, "validation.preflight.outcome")
	require.NotNil(t, usageOutcome, "expected outcome in usage attributes")
	require.Equal(t, preflightOutcomeWarningsAccepted, usageOutcome.Value.AsString())

	usageDiag := findSpanAttribute(
		usageAttrs, "validation.preflight.diagnostics",
	)
	require.NotNil(t, usageDiag, "expected diagnostics in usage attributes")
	require.Equal(t, diagnosticIDs, usageDiag.Value.AsStringSlice())
}

func TestSetPreflightOutcome_AllOutcomeValues(t *testing.T) {
	tests := []struct {
		name          string
		outcome       string
		diagnosticIDs []string
	}{
		{
			name:          "passed",
			outcome:       preflightOutcomePassed,
			diagnosticIDs: nil,
		},
		{
			name:          "warnings accepted",
			outcome:       preflightOutcomeWarningsAccepted,
			diagnosticIDs: []string{"role_assignment_missing"},
		},
		{
			name:          "aborted by errors",
			outcome:       preflightOutcomeAbortedByErrors,
			diagnosticIDs: []string{"role_assignment_missing"},
		},
		{
			name:          "aborted by user",
			outcome:       preflightOutcomeAbortedByUser,
			diagnosticIDs: []string{"role_assignment_conditional"},
		},
		{
			name:          "skipped",
			outcome:       preflightOutcomeSkipped,
			diagnosticIDs: nil,
		},
		{
			name:          "error",
			outcome:       preflightOutcomeError,
			diagnosticIDs: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			span := &mocktracing.Span{}
			provider := &BicepProvider{}

			provider.setPreflightOutcome(span, tt.outcome, tt.diagnosticIDs)

			outcomeAttr := findSpanAttribute(
				span.Attributes, "validation.preflight.outcome",
			)
			require.NotNil(t, outcomeAttr)
			require.Equal(t, tt.outcome, outcomeAttr.Value.AsString())
		})
	}
}

// findSpanAttribute searches for an attribute by key and returns a pointer to it, or nil.
func findSpanAttribute(
	attrs []attribute.KeyValue,
	key attribute.Key,
) *attribute.KeyValue {
	for i := range attrs {
		if attrs[i].Key == key {
			return &attrs[i]
		}
	}
	return nil
}

func TestEvalParamEnvSubstUsesVirtualEnv(t *testing.T) {
	env := environment.NewWithValues("test-env", map[string]string{})
	virtualKey := "AZD_TEST_VIRTUAL_LAYER_OUTPUT"
	virtualValue := "layer1--WEBSITE_URL"
	virtualEnv := map[string]string{virtualKey: virtualValue}

	testCases := []struct {
		name  string
		value string
		want  string
	}{
		{
			name:  "simple substitution",
			value: "${AZD_TEST_VIRTUAL_LAYER_OUTPUT}",
			want:  "layer1--WEBSITE_URL",
		},
		{
			name:  "mixed expression substitution",
			value: "prefix-${AZD_TEST_VIRTUAL_LAYER_OUTPUT}-suffix",
			want:  "prefix-layer1--WEBSITE_URL-suffix",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, substResult, err := evalParamEnvSubst(
				tc.value,
				"principal-id",
				"ServicePrincipal",
				"testParam",
				env,
				virtualEnv,
			)

			require.NoError(t, err)
			require.Equal(t, tc.want, result)
			require.True(t, substResult.hasVirtualEnvVar)
			require.False(t, substResult.hasUnsetEnvVar)
			require.Empty(t, substResult.mappedEnvVars)
		})
	}
}

func TestParameters_IncludesRealEnvMappingsForMixedRefs(t *testing.T) {
	const (
		realEnvVarName = "AZD_TEST_REAL_SECRET"
		virtualEnvKey  = "AZD_TEST_VIRTUAL_LAYER_OUTPUT"
	)

	testCases := []struct {
		name    string
		envVars map[string]string
	}{
		{
			name:    "set real env var",
			envVars: map[string]string{realEnvVarName: "secret-value"},
		},
		{
			name:    "unset real env var",
			envVars: map[string]string{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(t.Context())

			armTemplate := azure.ArmTemplate{
				// nolint: lll
				Schema:         "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
				ContentVersion: "1.0.0.0",
				Parameters: azure.ArmTemplateParameterDefinitions{
					"environmentName": {Type: "string", DefaultValue: "test-env"},
					"location":        {Type: "string", DefaultValue: "westus2"},
					"mixedValue":      {Type: "string"},
				},
				Outputs: azure.ArmTemplateOutputs{},
			}

			infraProvider := createBicepProviderWithEnvAndMode(
				t,
				mockContext,
				armTemplate,
				tc.envVars,
				provisioning.ModeDestroy,
			)

			tmpInfraDir := filepath.Join(t.TempDir(), "infra")
			require.NoError(t, os.MkdirAll(tmpInfraDir, 0o755))

			require.NoError(t, os.WriteFile(filepath.Join(tmpInfraDir, "main.parameters.json"), []byte(`{
				"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
				"contentVersion": "1.0.0.0",
				"parameters": {
					"mixedValue": {
						"value": "${AZD_TEST_VIRTUAL_LAYER_OUTPUT}/${AZD_TEST_REAL_SECRET}"
					}
				}
			}`), 0o600))

			infraProvider.path = filepath.Join(tmpInfraDir, "main.bicep")
			infraProvider.options.VirtualEnv = map[string]string{
				virtualEnvKey: "layer1--WEBSITE_URL",
			}

			compileResult, err := infraProvider.compileBicep(*mockContext.Context)
			require.NoError(t, err)

			loadResult, err := infraProvider.loadParameters(*mockContext.Context, &compileResult.Template)
			require.NoError(t, err)
			require.Contains(t, loadResult.virtualMapping, "mixedValue")
			require.Equal(t, []string{realEnvVarName}, loadResult.envMapping["mixedValue"])
			require.NotContains(t, loadResult.envMapping["mixedValue"], virtualEnvKey)
			require.NotContains(t, loadResult.parameters, "mixedValue")

			parameters, err := infraProvider.Parameters(*mockContext.Context)
			require.NoError(t, err)
			require.Len(t, parameters, 1)
			require.Equal(t, provisioning.Parameter{
				Name:               "mixedValue",
				Secret:             false,
				Value:              nil,
				EnvVarMapping:      []string{realEnvVarName},
				LocalPrompt:        false,
				UsingEnvVarMapping: true,
			}, parameters[0])
		})
	}
}

func TestEnsureParametersSkipsVirtualEnvMappedRequiredParameters(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	armTemplate := azure.ArmTemplate{
		Schema:         "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
		ContentVersion: "1.0.0.0",
		Parameters: azure.ArmTemplateParameterDefinitions{
			"environmentName": {Type: "string", DefaultValue: "test-env"},
			"location":        {Type: "string", DefaultValue: "westus2"},
			"dependentValue":  {Type: "string"},
			"compositeValue":  {Type: "string"},
		},
		Outputs: azure.ArmTemplateOutputs{},
	}

	infraProvider := createBicepProviderWithEnvAndMode(
		t,
		mockContext,
		armTemplate,
		map[string]string{},
		provisioning.ModeDestroy,
	)

	tmpInfraDir := filepath.Join(t.TempDir(), "infra")
	require.NoError(t, os.MkdirAll(tmpInfraDir, 0o755))

	const virtualEnvKey = "AZD_TEST_VIRTUAL_LAYER_OUTPUT"
	require.NoError(t, os.WriteFile(filepath.Join(tmpInfraDir, "main.parameters.json"), []byte(`{
		"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
		"contentVersion": "1.0.0.0",
		"parameters": {
			"dependentValue": {
				"value": "${AZD_TEST_VIRTUAL_LAYER_OUTPUT}"
			},
			"compositeValue": {
				"value": "prefix-${AZD_TEST_VIRTUAL_LAYER_OUTPUT}-suffix"
			}
		}
	}`), 0o600))

	infraProvider.path = filepath.Join(tmpInfraDir, "main.bicep")
	infraProvider.options.VirtualEnv = map[string]string{
		virtualEnvKey: "layer1--WEBSITE_URL",
	}

	compileResult, err := infraProvider.compileBicep(*mockContext.Context)
	require.NoError(t, err)

	loadResult, err := infraProvider.loadParameters(*mockContext.Context, &compileResult.Template)
	require.NoError(t, err)
	require.Contains(t, loadResult.virtualMapping, "dependentValue")
	require.Contains(t, loadResult.virtualMapping, "compositeValue")
	require.NotContains(t, loadResult.parameters, "dependentValue")
	require.NotContains(t, loadResult.parameters, "compositeValue")

	configuredParameters, err := infraProvider.ensureParameters(*mockContext.Context, compileResult.Template)
	require.NoError(t, err)
	require.NotContains(t, configuredParameters, "dependentValue")
	require.NotContains(t, configuredParameters, "compositeValue")
}

func TestPlannedOutputsSkipsSecureOutputs(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	armTemplate := azure.ArmTemplate{
		Schema:         "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
		ContentVersion: "1.0.0.0",
		Outputs: azure.ArmTemplateOutputs{
			"publicUrl": {
				Type: "string",
			},
			"connectionString": {
				Type: "secureString",
			},
			"config": {
				Type: "object",
			},
			"secretConfig": {
				Type: "secureObject",
			},
		},
	}

	infraProvider := createBicepProviderWithEnv(t, mockContext, armTemplate, map[string]string{})

	outputs, err := infraProvider.PlannedOutputs(*mockContext.Context)
	require.NoError(t, err)
	require.ElementsMatch(t, []provisioning.PlannedOutput{
		{Name: "publicUrl"},
		{Name: "config"},
	}, outputs)
}

// TestBicepProviderName verifies the provider name is returned.
func TestBicepProviderName(t *testing.T) {
	t.Parallel()

	p := &BicepProvider{}
	require.Equal(t, "Bicep", p.Name())
}

// TestParametersHash verifies hash behaviour across equal and different inputs.
func TestParametersHash(t *testing.T) {
	t.Parallel()

	defs := azure.ArmTemplateParameterDefinitions{
		"foo": {Type: "string", DefaultValue: "a"},
		"bar": {Type: "int", DefaultValue: 1},
	}

	t.Run("DeterministicWithDefaults", func(t *testing.T) {
		t.Parallel()
		h1, err := parametersHash(defs, azure.ArmParameters{})
		require.NoError(t, err)
		h2, err := parametersHash(defs, azure.ArmParameters{})
		require.NoError(t, err)
		require.Equal(t, h1, h2)
		require.NotEmpty(t, h1)
	})

	t.Run("ProvidedValueOverridesDefault", func(t *testing.T) {
		t.Parallel()
		hDefault, err := parametersHash(defs, azure.ArmParameters{})
		require.NoError(t, err)
		hOverridden, err := parametersHash(defs, azure.ArmParameters{
			"foo": {Value: "b"},
		})
		require.NoError(t, err)
		require.NotEqual(t, hDefault, hOverridden)
	})

	t.Run("SameFinalValueProducesSameHash", func(t *testing.T) {
		t.Parallel()
		h1, err := parametersHash(defs, azure.ArmParameters{"foo": {Value: "a"}})
		require.NoError(t, err)
		h2, err := parametersHash(defs, azure.ArmParameters{})
		require.NoError(t, err)
		require.Equal(t, h1, h2)
	})
}

// TestPrevDeploymentEqualToCurrent exhaustively covers the negative branches.
func TestPrevDeploymentEqualToCurrent(t *testing.T) {
	t.Parallel()

	templateHash := "TEMPLATE_HASH"
	paramsHash := "PARAMS_HASH"

	matchingTags := func() map[string]*string {
		return map[string]*string{
			azure.TagKeyAzdDeploymentStateParamHashName: new(paramsHash),
		}
	}

	cases := []struct {
		name string
		prev *azapi.ResourceDeployment
		want bool
	}{
		{
			name: "NilPrev",
			prev: nil,
			want: false,
		},
		{
			name: "NoTags",
			prev: &azapi.ResourceDeployment{TemplateHash: new(templateHash)},
			want: false,
		},
		{
			name: "DifferentTemplateHash",
			prev: &azapi.ResourceDeployment{
				TemplateHash: new("OTHER"),
				Tags:         matchingTags(),
			},
			want: false,
		},
		{
			name: "MissingParamHashTag",
			prev: &azapi.ResourceDeployment{
				TemplateHash: new(templateHash),
				Tags:         map[string]*string{"unrelated": new("x")},
			},
			want: false,
		},
		{
			name: "DifferentParamHash",
			prev: &azapi.ResourceDeployment{
				TemplateHash: new(templateHash),
				Tags: map[string]*string{
					azure.TagKeyAzdDeploymentStateParamHashName: new("DIFF"),
				},
			},
			want: false,
		},
		{
			name: "Equal",
			prev: &azapi.ResourceDeployment{
				TemplateHash: new(templateHash),
				Tags:         matchingTags(),
			},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := prevDeploymentEqualToCurrent(tc.prev, templateHash, paramsHash)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestLogDS simply ensures logDS does not panic for the common formatting paths.
func TestLogDS(t *testing.T) {
	t.Parallel()
	require.NotPanics(t, func() {
		logDS("plain message")
		logDS("formatted %s %d", "value", 1)
	})
}

// TestConvertPropertyChanges covers nil inputs, nil entries, recursion, and type mapping.
func TestConvertPropertyChanges(t *testing.T) {
	t.Parallel()

	t.Run("Nil", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, convertPropertyChanges(nil))
	})

	t.Run("SkipsNilEntries", func(t *testing.T) {
		t.Parallel()
		changes := []*armresources.WhatIfPropertyChange{nil, nil}
		result := convertPropertyChanges(changes)
		require.Empty(t, result)
	})

	t.Run("ConvertsAndRecurses", func(t *testing.T) {
		t.Parallel()
		modify := armresources.PropertyChangeTypeModify
		create := armresources.PropertyChangeTypeCreate
		childPath := "child"
		parent := &armresources.WhatIfPropertyChange{
			Path:               new("parent"),
			PropertyChangeType: &modify,
			Before:             "old",
			After:              "new",
			Children: []*armresources.WhatIfPropertyChange{
				{
					Path:               &childPath,
					PropertyChangeType: &create,
					After:              "created",
				},
			},
		}
		result := convertPropertyChanges([]*armresources.WhatIfPropertyChange{parent})
		require.Len(t, result, 1)
		require.Equal(t, "parent", result[0].Path)
		require.Equal(t, "old", result[0].Before)
		require.Equal(t, "new", result[0].After)
		require.Equal(t, provisioning.PropertyChangeType(modify), result[0].ChangeType)
		require.Len(t, result[0].Children, 1)
		require.Equal(t, "child", result[0].Children[0].Path)
		require.Equal(t, provisioning.PropertyChangeType(create), result[0].Children[0].ChangeType)
	})

	t.Run("NilPathAndChangeType", func(t *testing.T) {
		t.Parallel()
		change := &armresources.WhatIfPropertyChange{}
		result := convertPropertyChanges([]*armresources.WhatIfPropertyChange{change})
		require.Len(t, result, 1)
		require.Equal(t, "", result[0].Path)
		require.Nil(t, result[0].Before)
		require.Nil(t, result[0].After)
	})
}

// TestItemsCountAsText covers the normal and panic paths.
func TestItemsCountAsText(t *testing.T) {
	t.Parallel()

	t.Run("PanicsOnEmpty", func(t *testing.T) {
		t.Parallel()
		require.Panics(t, func() {
			_ = itemsCountAsText(nil)
		})
	})

	t.Run("SkipsZeroCountItems", func(t *testing.T) {
		t.Parallel()
		text := itemsCountAsText([]itemToPurge{
			{resourceType: "Key Vaults", count: 0},
			{resourceType: "App Configurations", count: 2},
		})
		require.Contains(t, text, "2 App Configurations")
		require.NotContains(t, text, "Key Vaults")
	})

	t.Run("FormatsSingleItem", func(t *testing.T) {
		t.Parallel()
		text := itemsCountAsText([]itemToPurge{
			{resourceType: "Managed HSMs", count: 1},
		})
		require.Contains(t, text, "1 Managed HSMs")
	})
}

// TestGetDeploymentOptions exercises the deployment-option prompt helper.
func TestGetDeploymentOptions(t *testing.T) {
	t.Parallel()

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		got := getDeploymentOptions(nil)
		require.Empty(t, got)
	})

	t.Run("Formats", func(t *testing.T) {
		t.Parallel()
		ts := time.Date(2024, 5, 10, 14, 30, 0, 0, time.UTC)
		deployments := []*azapi.ResourceDeployment{
			{Name: "dep-1", Timestamp: ts},
			{Name: "dep-2", Timestamp: ts.Add(time.Hour)},
		}
		got := getDeploymentOptions(deployments)
		require.Len(t, got, 2)
		require.Contains(t, got[0], "1.")
		require.Contains(t, got[0], "dep-1")
		require.Contains(t, got[1], "2.")
		require.Contains(t, got[1], "dep-2")
	})
}

// TestConvertToDeployment verifies parameter and output conversion.
func TestConvertToDeployment(t *testing.T) {
	t.Parallel()

	p := &BicepProvider{}
	tpl := azure.ArmTemplate{
		Parameters: azure.ArmTemplateParameterDefinitions{
			"stringParam": {Type: "string", DefaultValue: "hello"},
			"intParam":    {Type: "int", DefaultValue: 1},
		},
		Outputs: azure.ArmTemplateOutputs{
			"endpoint": {Type: "string", Value: "https://example"},
		},
	}

	dep := p.convertToDeployment(tpl)
	require.Len(t, dep.Parameters, 2)
	require.Equal(t, "hello", dep.Parameters["stringParam"].DefaultValue)
	require.Equal(t, string(provisioning.ParameterTypeString), dep.Parameters["stringParam"].Type)
	require.Equal(t, string(provisioning.ParameterTypeNumber), dep.Parameters["intParam"].Type)
	require.Len(t, dep.Outputs, 1)
	require.Equal(t, "https://example", dep.Outputs["endpoint"].Value)
	require.Equal(t, provisioning.ParameterTypeString, dep.Outputs["endpoint"].Type)
}

// TestMustSetParamAsConfig covers the regular and secure paths.
func TestMustSetParamAsConfig(t *testing.T) {
	t.Parallel()

	t.Run("PlainValue", func(t *testing.T) {
		t.Parallel()
		cfg := config.NewEmptyConfig()
		mustSetParamAsConfig("plainParam", "some-value", cfg, false)
		got, has := cfg.Get(configInfraParametersKey + "plainParam")
		require.True(t, has)
		require.Equal(t, "some-value", got)
	})

	t.Run("SecureValue", func(t *testing.T) {
		t.Parallel()
		cfg := config.NewEmptyConfig()
		mustSetParamAsConfig("secureParam", "secret", cfg, true)
		// Secret values are retrieved via Get but stored as references.
		got, has := cfg.Get(configInfraParametersKey + "secureParam")
		require.True(t, has)
		require.NotNil(t, got)
	})

	t.Run("SecureNonStringPanics", func(t *testing.T) {
		t.Parallel()
		cfg := config.NewEmptyConfig()
		require.Panics(t, func() {
			mustSetParamAsConfig("bad", 123, cfg, true)
		})
	})
}

// TestEvalCommandSubstitutionPassthrough verifies that values without a command
// invocation are returned unchanged.
func TestEvalCommandSubstitutionPassthrough(t *testing.T) {
	t.Parallel()

	p := &BicepProvider{}
	for _, input := range []string{"", "plain-value", "https://example.com"} {
		out, err := p.evalCommandSubstitution(t.Context(), input)
		require.NoError(t, err)
		require.Equal(t, input, out)
	}
}

// TestCreateDeploymentFromArmDeployment verifies scope dispatch and errors.
func TestCreateDeploymentFromArmDeployment(t *testing.T) {
	t.Parallel()

	t.Run("SubscriptionScope", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		prepareBicepMocks(mockContext)
		p := createBicepProvider(t, mockContext)
		scope := p.deploymentManager.SubscriptionScope("SUBSCRIPTION_ID", "westus2")
		dep, err := p.createDeploymentFromArmDeployment(scope, "dep-name")
		require.NoError(t, err)
		require.NotNil(t, dep)
	})

	t.Run("ResourceGroupScope", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		prepareBicepMocks(mockContext)
		p := createBicepProvider(t, mockContext)
		scope := p.deploymentManager.ResourceGroupScope("SUBSCRIPTION_ID", "RG")
		dep, err := p.createDeploymentFromArmDeployment(scope, "dep-name")
		require.NoError(t, err)
		require.NotNil(t, dep)
	})

	t.Run("UnsupportedScope", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		prepareBicepMocks(mockContext)
		p := createBicepProvider(t, mockContext)
		_, err := p.createDeploymentFromArmDeployment(unsupportedScope{}, "dep-name")
		require.Error(t, err)
	})
}

// unsupportedScope is a stand-in that implements infra.Scope but is neither a
// resource group nor a subscription scope, exercising the error branch of
// createDeploymentFromArmDeployment.
type unsupportedScope struct{}

func (unsupportedScope) SubscriptionId() string { return "" }
func (unsupportedScope) ListDeployments(ctx context.Context) ([]*azapi.ResourceDeployment, error) {
	return nil, errors.New("not implemented")
}
func (unsupportedScope) Deployment(string) infra.Deployment { return nil }

// TestInferScopeFromEnv covers both scope branches.
func TestInferScopeFromEnv(t *testing.T) {
	t.Parallel()

	t.Run("ResourceGroupScopeWhenEnvSet", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		prepareBicepMocks(mockContext)
		p := createBicepProvider(t, mockContext)
		p.env.DotenvSet(environment.ResourceGroupEnvVarName, "my-rg")

		scope, err := p.inferScopeFromEnv()
		require.NoError(t, err)
		rg, ok := scope.(*infra.ResourceGroupScope)
		require.True(t, ok, "expected ResourceGroupScope")
		require.Equal(t, "my-rg", rg.ResourceGroupName())
	})

	t.Run("SubscriptionScopeByDefault", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		prepareBicepMocks(mockContext)
		p := createBicepProvider(t, mockContext)

		scope, err := p.inferScopeFromEnv()
		require.NoError(t, err)
		_, ok := scope.(*infra.SubscriptionScope)
		require.True(t, ok, "expected SubscriptionScope, got %T", scope)
	})
}

// TestScopeForTemplate covers subscription, resource group, and unsupported scopes.
func TestScopeForTemplate(t *testing.T) {
	t.Parallel()

	t.Run("Subscription", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		prepareBicepMocks(mockContext)
		p := createBicepProvider(t, mockContext)
		tpl := azure.ArmTemplate{
			Schema: "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
		}
		scope, err := p.scopeForTemplate(tpl)
		require.NoError(t, err)
		_, ok := scope.(*infra.SubscriptionScope)
		require.True(t, ok)
	})

	t.Run("ResourceGroup", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		prepareBicepMocks(mockContext)
		p := createBicepProvider(t, mockContext)
		p.env.DotenvSet(environment.ResourceGroupEnvVarName, "my-rg")
		tpl := azure.ArmTemplate{
			Schema: "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
		}
		scope, err := p.scopeForTemplate(tpl)
		require.NoError(t, err)
		rg, ok := scope.(*infra.ResourceGroupScope)
		require.True(t, ok)
		require.Equal(t, "my-rg", rg.ResourceGroupName())
	})

	t.Run("Unsupported", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		prepareBicepMocks(mockContext)
		p := createBicepProvider(t, mockContext)
		// Empty schema causes TargetScope() to return an unsupported scope or error.
		tpl := azure.ArmTemplate{Schema: "https://example.com/unknown.json#"}
		_, err := p.scopeForTemplate(tpl)
		require.Error(t, err)
	})
}

// TestDefinitionName covers the happy path and edge cases for the helper.
func TestDefinitionName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input, want string
	}{
		{"#/definitions/MyType", "MyType"},
		{"/definitions/Foo", "Foo"},
		{"Bar", "Bar"},
	}
	for _, tc := range cases {
		got, err := definitionName(tc.input)
		require.NoError(t, err)
		require.Equal(t, tc.want, got)
	}
}

// TestDefaultPromptValue exercises metadata-driven and allowed-values defaults.
func TestDefaultPromptValue(t *testing.T) {
	t.Parallel()

	t.Run("NilWhenNoMetadataOrAllowedValues", func(t *testing.T) {
		t.Parallel()
		got := defaultPromptValue(azure.ArmTemplateParameterDefinition{})
		require.Nil(t, got)
	})

	t.Run("AllowedValuesFirstString", func(t *testing.T) {
		t.Parallel()
		vals := []any{"westus2", "eastus"}
		got := defaultPromptValue(azure.ArmTemplateParameterDefinition{
			AllowedValues: &vals,
		})
		require.NotNil(t, got)
		require.Equal(t, "westus2", *got)
	})

	t.Run("AllowedValuesNonStringIgnored", func(t *testing.T) {
		t.Parallel()
		vals := []any{42, "eastus"}
		got := defaultPromptValue(azure.ArmTemplateParameterDefinition{
			AllowedValues: &vals,
		})
		require.Nil(t, got)
	})

	t.Run("AzdMetadataLocationDefault", func(t *testing.T) {
		t.Parallel()
		locationType := azure.AzdMetadataTypeLocation
		def := azure.ArmTemplateParameterDefinition{
			Metadata: map[string]json.RawMessage{
				"azd": mustMarshal(t, azure.AzdMetadata{
					Type:    &locationType,
					Default: "westus3",
				}),
			},
		}
		got := defaultPromptValue(def)
		require.NotNil(t, got)
		require.Equal(t, "westus3", *got)
	})
}

// TestLocationParameterFilterImpl verifies allow-list filtering.
func TestLocationParameterFilterImpl(t *testing.T) {
	t.Parallel()

	require.True(t, locationParameterFilterImpl(nil, account.Location{Name: "westus2"}))
	require.True(t, locationParameterFilterImpl(
		[]string{"eastus", "westus2"}, account.Location{Name: "westus2"}))
	require.False(t, locationParameterFilterImpl(
		[]string{"eastus"}, account.Location{Name: "westus2"}))
	require.False(t, locationParameterFilterImpl(
		[]string{}, account.Location{Name: "westus2"}))
}

// TestGenerateDeploymentObjectUnsupportedScope verifies the error path.
func TestGenerateDeploymentObjectUnsupportedScope(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())
	prepareBicepMocks(mockContext)
	p := createBicepProvider(t, mockContext)

	tpl := azure.ArmTemplate{Schema: "https://example.com/unknown.json#"}
	plan := &compileBicepResult{Template: tpl}
	_, err := p.generateDeploymentObject(plan)
	require.Error(t, err)
}

// TestGenerateDeploymentObjectResourceGroup covers the RG path.
func TestGenerateDeploymentObjectResourceGroup(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())
	prepareBicepMocks(mockContext)
	p := createBicepProvider(t, mockContext)
	p.env.DotenvSet(environment.ResourceGroupEnvVarName, "rg-alpha")

	tpl := azure.ArmTemplate{
		Schema: "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
	}
	plan := &compileBicepResult{Template: tpl}
	dep, err := p.generateDeploymentObject(plan)
	require.NoError(t, err)
	require.NotNil(t, dep)
	require.Contains(t, dep.Name(), "test-env")
}

// TestGenerateDeploymentObjectWithLayer verifies the layer suffix is appended
// to the deployment base name.
func TestGenerateDeploymentObjectWithLayer(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())
	prepareBicepMocks(mockContext)
	p := createBicepProvider(t, mockContext)
	p.layer = "api"

	tpl := azure.ArmTemplate{
		Schema: "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
	}
	plan := &compileBicepResult{Template: tpl}
	dep, err := p.generateDeploymentObject(plan)
	require.NoError(t, err)
	require.NotNil(t, dep)
	require.Contains(t, dep.Name(), "test-env-api")
}

// helper for TestDefaultPromptValue azd metadata sub-test.
func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

// TestCognitiveAccountsByKind verifies grouping by kind and the FormRecognizer rename.
func TestCognitiveAccountsByKind(t *testing.T) {
	t.Parallel()

	input := map[string][]armcognitiveservices.Account{
		"rg1": {
			{Kind: new("OpenAI")},
			{Kind: new("FormRecognizer")},
		},
		"rg2": {
			{Kind: new("OpenAI")},
		},
	}

	got := cognitiveAccountsByKind(input)

	require.Contains(t, got, "OpenAI")
	require.Len(t, got["OpenAI"], 2)
	require.Contains(t, got, "Document Intelligence")
	require.Len(t, got["Document Intelligence"], 1)
	require.NotContains(t, got, "FormRecognizer")
}

// TestAutoGenerate covers missing-config error and a successful generation path.
func TestAutoGenerate(t *testing.T) {
	t.Parallel()

	t.Run("MissingConfigReturnsError", func(t *testing.T) {
		t.Parallel()
		_, err := autoGenerate("param", azure.AzdMetadata{})
		require.Error(t, err)
	})

	t.Run("GeneratesValue", func(t *testing.T) {
		t.Parallel()
		v, err := autoGenerate("param", azure.AzdMetadata{
			AutoGenerateConfig: &azure.AutoGenInput{Length: 16},
		})
		require.NoError(t, err)
		require.Len(t, v, 16)
	})
}

// TestUsageNameDetailsFromString covers all parsing branches.
func TestUsageNameDetailsFromString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		wantErr  bool
		wantName string
		wantCap  float64
	}{
		{name: "Empty", input: "   ", wantErr: true},
		{name: "SinglePart", input: "OpenAI.S0.AccountCount", wantName: "OpenAI.S0.AccountCount", wantCap: 1},
		{name: "TwoParts", input: "OpenAI.Tokens , 10", wantName: "OpenAI.Tokens", wantCap: 10},
		{name: "TooManyParts", input: "a, 1, 2", wantErr: true},
		{name: "InvalidCapacity", input: "x, abc", wantErr: true},
		{name: "ZeroCapacity", input: "x, 0", wantErr: true},
		{name: "NegativeCapacity", input: "x, -5", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := usageNameDetailsFromString(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantName, got.UsageName)
			require.Equal(t, tc.wantCap, got.Capacity)
		})
	}
}

// TestArmTemplateResourcesUnmarshalJSON exercises both the array and
// symbolic-name (map) forms of the "resources" member and the error path.
func TestArmTemplateResourcesUnmarshalJSON(t *testing.T) {
	t.Parallel()

	t.Run("ArrayForm", func(t *testing.T) {
		t.Parallel()
		var r armTemplateResources
		err := json.Unmarshal([]byte(`[{"type":"Microsoft.Storage/storageAccounts","name":"s1"}]`), &r)
		require.NoError(t, err)
		require.Len(t, r, 1)
		require.Equal(t, "Microsoft.Storage/storageAccounts", r[0].Type)
	})

	t.Run("MapForm", func(t *testing.T) {
		t.Parallel()
		var r armTemplateResources
		err := json.Unmarshal([]byte(`{"sym":{"type":"Microsoft.Storage/storageAccounts","name":"s1"}}`), &r)
		require.NoError(t, err)
		require.Len(t, r, 1)
	})

	t.Run("Invalid", func(t *testing.T) {
		t.Parallel()
		var r armTemplateResources
		err := json.Unmarshal([]byte(`"a string, not an array or object"`), &r)
		require.Error(t, err)
	})
}

// TestArmParameterFileValue covers the type-coercion matrix for the helper.
func TestArmParameterFileValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		paramType provisioning.ParameterType
		value     any
		defValue  any
		want      any
	}{
		{name: "NilPassthrough", paramType: provisioning.ParameterTypeString, value: nil, want: nil},
		{name: "NonStringPassthrough", paramType: provisioning.ParameterTypeNumber, value: 42, want: 42},
		{name: "BoolFromString", paramType: provisioning.ParameterTypeBoolean, value: "true", want: true},
		{name: "BoolFromStringBad", paramType: provisioning.ParameterTypeBoolean, value: "not-bool", want: nil},
		{name: "NumberFromString", paramType: provisioning.ParameterTypeNumber, value: "123", want: int64(123)},
		{name: "NumberFromStringBad", paramType: provisioning.ParameterTypeNumber, value: "abc", want: nil},
		{name: "StringNonEmpty", paramType: provisioning.ParameterTypeString, value: "hello", want: "hello"},
		{name: "StringEmptyNoDefault", paramType: provisioning.ParameterTypeString, value: "", want: nil},
		{
			name:      "StringEmptyWithMatchingDefault",
			paramType: provisioning.ParameterTypeString,
			value:     "", defValue: "",
			want: nil,
		},
		{
			name:      "DefaultCase",
			paramType: provisioning.ParameterTypeArray,
			value:     "x",
			want:      "x",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := armParameterFileValue(tc.paramType, tc.value, tc.defValue)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestIsValueAssignableToParameterTypeAdditional covers extra branches of
// isValueAssignableToParameterType not covered by the existing test.
func TestIsValueAssignableToParameterTypeAdditional(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		paramType provisioning.ParameterType
		value     any
		want      bool
	}{
		{name: "ArrayOk", paramType: provisioning.ParameterTypeArray, value: []any{1, 2}, want: true},
		{name: "ArrayNo", paramType: provisioning.ParameterTypeArray, value: "not-array", want: false},
		{name: "BoolOk", paramType: provisioning.ParameterTypeBoolean, value: true, want: true},
		{name: "BoolNo", paramType: provisioning.ParameterTypeBoolean, value: "true", want: false},
		{name: "NumberIntOk", paramType: provisioning.ParameterTypeNumber, value: 5, want: true},
		{name: "NumberUintOk", paramType: provisioning.ParameterTypeNumber, value: uint(5), want: true},
		{name: "NumberFloatOk", paramType: provisioning.ParameterTypeNumber, value: 5.0, want: true},
		{name: "NumberFloatFrac", paramType: provisioning.ParameterTypeNumber, value: 5.5, want: false},
		{name: "NumberJSONOk", paramType: provisioning.ParameterTypeNumber, value: json.Number("7"), want: true},
		{name: "NumberBad", paramType: provisioning.ParameterTypeNumber, value: "5", want: false},
		{name: "ObjectOk", paramType: provisioning.ParameterTypeObject, value: map[string]any{"a": 1}, want: true},
		{name: "ObjectNo", paramType: provisioning.ParameterTypeObject, value: []any{}, want: false},
		{name: "StringOk", paramType: provisioning.ParameterTypeString, value: "hi", want: true},
		{name: "StringNo", paramType: provisioning.ParameterTypeString, value: 1, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := isValueAssignableToParameterType(tc.paramType, tc.value)
			require.Equal(t, tc.want, got)
		})
	}

	t.Run("UnknownTypePanics", func(t *testing.T) {
		t.Parallel()
		require.Panics(t, func() {
			isValueAssignableToParameterType(provisioning.ParameterType("bogus"), 1)
		})
	})
}

// TestEvalParamEnvSubst exercises the principal/location/virtualEnv/env var branches.
func TestEvalParamEnvSubst(t *testing.T) {
	t.Parallel()

	env := environment.NewWithValues("test", map[string]string{
		"MY_VAR": "my-value",
	})

	t.Run("PrincipalIdAndType", func(t *testing.T) {
		t.Parallel()
		out, res, err := evalParamEnvSubst(
			"${AZURE_PRINCIPAL_ID}-${AZURE_PRINCIPAL_TYPE}",
			"pid-123", "User", "param", env, nil)
		require.NoError(t, err)
		require.Equal(t, "pid-123-User", out)
		require.False(t, res.hasUnsetEnvVar)
	})

	t.Run("LocationIsTracked", func(t *testing.T) {
		t.Parallel()
		_, res, err := evalParamEnvSubst(
			"${AZURE_LOCATION}", "", "", "locParam", env, nil)
		require.NoError(t, err)
		require.Contains(t, res.parametersMappedToAzureLocation, "locParam")
	})

	t.Run("VirtualEnvOverride", func(t *testing.T) {
		t.Parallel()
		out, res, err := evalParamEnvSubst(
			"${FOO}", "", "", "p", env, map[string]string{"FOO": "bar"})
		require.NoError(t, err)
		require.Equal(t, "bar", out)
		require.True(t, res.hasVirtualEnvVar)
	})

	t.Run("EnvVarLookup", func(t *testing.T) {
		t.Parallel()
		out, res, err := evalParamEnvSubst(
			"${MY_VAR}", "", "", "p", env, nil)
		require.NoError(t, err)
		require.Equal(t, "my-value", out)
		require.False(t, res.hasUnsetEnvVar)
	})

	t.Run("UnsetEnvVar", func(t *testing.T) {
		t.Parallel()
		_, res, err := evalParamEnvSubst(
			"${NOT_SET}", "", "", "p", env, nil)
		require.NoError(t, err)
		require.True(t, res.hasUnsetEnvVar)
	})
}

// TestResolveResourceGroupLocation covers the short-circuit paths (empty sub id, no RG env var).
func TestResolveResourceGroupLocation(t *testing.T) {
	t.Parallel()

	t.Run("EmptySubscriptionId", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		prepareBicepMocks(mockContext)
		p := createBicepProvider(t, mockContext)
		loc := p.resolveResourceGroupLocation(t.Context(), "")
		require.Equal(t, "", loc)
	})

	t.Run("NoResourceGroupEnvVar", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		prepareBicepMocks(mockContext)
		p := createBicepProvider(t, mockContext)
		// Don't set AZURE_RESOURCE_GROUP; expect empty result.
		loc := p.resolveResourceGroupLocation(t.Context(), "SUBSCRIPTION_ID")
		require.Equal(t, "", loc)
	})
}

// TestConvertIntAndJsonHelpers covers the small prompt converter helpers.
func TestConvertIntAndJsonHelpers(t *testing.T) {
	t.Parallel()

	t.Run("ConvertStringPassthrough", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, "abc", convertString("abc"))
	})

	t.Run("ConvertIntSuccess", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, 42, convertInt("42"))
	})

	t.Run("ConvertIntPanicOnBadInput", func(t *testing.T) {
		t.Parallel()
		require.Panics(t, func() { convertInt("not-a-number") })
	})

	t.Run("ConvertJsonArraySuccess", func(t *testing.T) {
		t.Parallel()
		got := convertJson[[]string](`["a","b"]`)
		require.Equal(t, []string{"a", "b"}, got)
	})

	t.Run("ConvertJsonObjectSuccess", func(t *testing.T) {
		t.Parallel()
		got := convertJson[map[string]any](`{"k":1}`)
		require.Equal(t, float64(1), got["k"])
	})

	t.Run("ConvertJsonPanicOnBadInput", func(t *testing.T) {
		t.Parallel()
		require.Panics(t, func() { convertJson[map[string]any](`not-json`) })
	})
}

// TestNewLocalArmPreflightAndAddCheck covers the constructor and AddCheck append path.
func TestNewLocalArmPreflightAndAddCheck(t *testing.T) {
	t.Parallel()

	pf := newLocalArmPreflight("infra/main.bicep", nil, nil, "westus2")
	require.NotNil(t, pf)
	require.Equal(t, "infra/main.bicep", pf.modulePath)
	require.Equal(t, "westus2", pf.envLocation)
	require.Nil(t, pf.target)
	require.Empty(t, pf.checks)

	// AddCheck appends; verify count grows.
	noopFn := func(ctx context.Context, valCtx *validationContext) ([]PreflightCheckResult, error) {
		return nil, nil
	}
	pf.AddCheck(PreflightCheck{RuleID: "rule1", Fn: noopFn})
	pf.AddCheck(PreflightCheck{RuleID: "rule2", Fn: noopFn})
	require.Len(t, pf.checks, 2)
}

// TestLatestDeploymentResult covers the success path through the deployment manager
// by reusing the package's mockedScope which returns 3 tagged deployments.
func TestLatestDeploymentResult(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())
	prepareBicepMocks(mockContext)
	p := createBicepProvider(t, mockContext)

	scope := &mockedScope{
		baseDate: "1989-10-31",
		envTag:   p.env.Name(),
	}

	dep, err := p.latestDeploymentResult(t.Context(), scope)
	require.NoError(t, err)
	require.NotNil(t, dep)
}

// TestLatestDeploymentResultError verifies error propagation from
// scope.ListDeployments.
func TestLatestDeploymentResultError(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())
	prepareBicepMocks(mockContext)
	p := createBicepProvider(t, mockContext)

	// unsupportedScope returns error from ListDeployments.
	_, err := p.latestDeploymentResult(t.Context(), unsupportedScope{})
	require.Error(t, err)
}

// TestDeploymentStateErrors exercises the error branches of deploymentState
// without requiring any HTTP mocks.
func TestDeploymentStateErrors(t *testing.T) {
	t.Parallel()

	t.Run("listDeploymentsError", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		prepareBicepMocks(mockContext)
		p := createBicepProvider(t, mockContext)

		_, err := p.deploymentState(
			t.Context(),
			&compileBicepResult{},
			unsupportedScope{},
			"hash",
		)
		require.Error(t, err)
	})
}

// TestValidateErrors exercises the error/skip paths of localArmPreflight.validate
// without depending on a real Bicep snapshot.
func TestValidateErrors(t *testing.T) {
	t.Parallel()

	t.Run("parseTemplateError", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		prepareBicepMocks(mockContext)
		p := createBicepProvider(t, mockContext)

		pre := newLocalArmPreflight("main.bicep", p.bicepCli, nil, "eastus2")
		// Pass invalid JSON to trigger parseTemplate error.
		_, _, err := pre.validate(
			t.Context(),
			mockContext.Console,
			azure.RawArmTemplate("not-json"),
			azure.ArmParameters{},
		)
		require.Error(t, err)
	})

	t.Run("snapshotUnavailableSkips", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		prepareBicepMocks(mockContext)
		// Mock bicep snapshot to fail; validate should treat it as a skip.
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return len(args.Args) > 0 && args.Args[0] == "snapshot"
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				return exec.RunResult{ExitCode: 1, Stderr: "snapshot not supported"},
					errors.New("snapshot not supported")
			})
		p := createBicepProvider(t, mockContext)

		// Minimal valid ARM template so parseTemplate succeeds.
		raw := azure.RawArmTemplate(
			`{"$schema":"x","contentVersion":"1.0",` +
				`"resources":[{"type":"Microsoft.Resources/deployments",` +
				`"name":"x","apiVersion":"2020-10-01"}]}`)

		pre := newLocalArmPreflight("nonexistent.bicepparam", p.bicepCli, nil, "")
		_, results, err := pre.validate(
			t.Context(),
			mockContext.Console,
			raw,
			azure.ArmParameters{},
		)
		require.NoError(t, err)
		require.Nil(t, results)
	})

	t.Run("bicepModulePathCreatesTempParam", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		prepareBicepMocks(mockContext)
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return len(args.Args) > 0 && args.Args[0] == "snapshot"
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				return exec.RunResult{ExitCode: 1, Stderr: "snapshot not supported"},
					errors.New("snapshot not supported")
			})
		p := createBicepProvider(t, mockContext)

		raw := azure.RawArmTemplate(
			`{"$schema":"x","contentVersion":"1.0",` +
				`"resources":[{"type":"Microsoft.Resources/deployments",` +
				`"name":"x","apiVersion":"2020-10-01"}]}`)

		// Use a .bicep module path (in a writable tempdir) so validate must
		// create and clean up a temp .bicepparam file.
		moduleDir := t.TempDir()
		modulePath := moduleDir + "/main.bicep"

		pre := newLocalArmPreflight(modulePath, p.bicepCli, nil, "eastus2")
		_, results, err := pre.validate(
			t.Context(),
			mockContext.Console,
			raw,
			azure.ArmParameters{
				"foo": {Value: "bar"},
			},
		)
		require.NoError(t, err)
		require.Nil(t, results)
	})
}

// TestPurgeHelpersEmptyInputs exercises the fast-path (empty input) and
// skip-path branches of the purge/getToPurge helpers. None of these paths
// require HTTP mocks.
func TestPurgeHelpersEmptyInputs(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())
	prepareBicepMocks(mockContext)
	p := createBicepProvider(t, mockContext)
	ctx := t.Context()
	empty := map[string][]*azapi.Resource{}

	t.Run("getKeyVaults", func(t *testing.T) {
		got, err := p.getKeyVaults(ctx, empty)
		require.NoError(t, err)
		require.Empty(t, got)
	})
	t.Run("getKeyVaultsToPurge", func(t *testing.T) {
		got, err := p.getKeyVaultsToPurge(ctx, empty)
		require.NoError(t, err)
		require.Empty(t, got)
	})
	t.Run("getManagedHSMs", func(t *testing.T) {
		got, err := p.getManagedHSMs(ctx, empty)
		require.NoError(t, err)
		require.Empty(t, got)
	})
	t.Run("getManagedHSMsToPurge", func(t *testing.T) {
		got, err := p.getManagedHSMsToPurge(ctx, empty)
		require.NoError(t, err)
		require.Empty(t, got)
	})
	t.Run("getCognitiveAccountsToPurge", func(t *testing.T) {
		got, err := p.getCognitiveAccountsToPurge(ctx, empty)
		require.NoError(t, err)
		require.Empty(t, got)
	})
	t.Run("getAppConfigsToPurge", func(t *testing.T) {
		got, err := p.getAppConfigsToPurge(ctx, empty)
		require.NoError(t, err)
		require.Empty(t, got)
	})
	t.Run("getApiManagementsToPurge", func(t *testing.T) {
		got, err := p.getApiManagementsToPurge(ctx, empty)
		require.NoError(t, err)
		require.Empty(t, got)
	})
	t.Run("purgeKeyVaultsEmpty", func(t *testing.T) {
		require.NoError(t, p.purgeKeyVaults(ctx, nil, true))
	})
	t.Run("purgeManagedHSMsEmpty", func(t *testing.T) {
		require.NoError(t, p.purgeManagedHSMs(ctx, nil, true))
	})
	t.Run("purgeCognitiveAccountsEmpty", func(t *testing.T) {
		require.NoError(t, p.purgeCognitiveAccounts(ctx, nil, true))
	})
	t.Run("purgeAppConfigsEmpty", func(t *testing.T) {
		require.NoError(t, p.purgeAppConfigs(ctx, nil, true))
	})
	t.Run("purgeAPIManagementEmpty", func(t *testing.T) {
		require.NoError(t, p.purgeAPIManagement(ctx, nil, true))
	})
	t.Run("forceDeleteLogAnalyticsWorkspacesEmpty", func(t *testing.T) {
		require.NoError(t, p.forceDeleteLogAnalyticsWorkspaces(ctx, nil))
	})
	t.Run("purgeItemsEmpty", func(t *testing.T) {
		require.NoError(t, p.purgeItems(ctx, nil, provisioning.NewDestroyOptions(false, false)))
	})
	t.Run("runPurgeAsStepSkipped", func(t *testing.T) {
		called := false
		err := p.runPurgeAsStep(ctx, "resource", "name", func() error {
			called = true
			return nil
		}, true /* skipped */)
		require.NoError(t, err)
		require.False(t, called, "step fn must not be called when skipped")
	})
	t.Run("runPurgeAsStepExecutes", func(t *testing.T) {
		called := false
		err := p.runPurgeAsStep(ctx, "resource", "name", func() error {
			called = true
			return nil
		}, false)
		require.NoError(t, err)
		require.True(t, called)
	})
}

// TestPurgeCognitiveAccountsValidationErrors verifies early-return errors for
// accounts missing required fields.
func TestPurgeCognitiveAccountsValidationErrors(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())
	prepareBicepMocks(mockContext)
	p := createBicepProvider(t, mockContext)
	ctx := t.Context()

	t.Run("missingName", func(t *testing.T) {
		err := p.purgeCognitiveAccounts(ctx, []cognitiveAccount{
			{account: armcognitiveservices.Account{}, resourceGroup: "rg"},
		}, false)
		require.Error(t, err)
	})
	t.Run("missingId", func(t *testing.T) {
		err := p.purgeCognitiveAccounts(ctx, []cognitiveAccount{
			{account: armcognitiveservices.Account{Name: new("n")}, resourceGroup: "rg"},
		}, false)
		require.Error(t, err)
	})
	t.Run("missingLocation", func(t *testing.T) {
		err := p.purgeCognitiveAccounts(ctx, []cognitiveAccount{
			{account: armcognitiveservices.Account{
				Name: new("n"),
				ID:   new("/subscriptions/x/resourceGroups/rg/providers/p/accounts/n"),
			}, resourceGroup: "rg"},
		}, false)
		require.Error(t, err)
	})
}
