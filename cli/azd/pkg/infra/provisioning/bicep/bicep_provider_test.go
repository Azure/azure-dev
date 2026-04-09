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
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/apimanagement/armapimanagement"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appconfiguration/armappconfiguration"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armlocks"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
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
	"github.com/azure/azure-dev/cli/azd/test/mocks/mocktracing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

func TestBicepPlan(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
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
	mockContext := mocks.NewMockContext(context.Background())
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
	mockContext := mocks.NewMockContext(context.Background())
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

	require.Equal(t, "value", plan.Parameters["stringParam"].Value)
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

		// Register credential provider so Tier 4 lock/resource checks work.
		mockContext.Container.MustRegisterSingleton(
			func() account.SubscriptionCredentialProvider {
				return mockaccount.SubscriptionCredentialProviderFunc(
					func(_ context.Context, _ string) (azcore.TokenCredential, error) {
						return mockContext.Credentials, nil
					},
				)
			},
		)

		// Register ARM client options so Tier 4 helpers use mock HTTP transport.
		mockContext.Container.MustRegisterSingleton(
			func() *arm.ClientOptions {
				return mockContext.ArmClientOptions
			},
		)

		// Tier 4 lock check: no locks on the RG.
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				strings.Contains(request.URL.Path, "providers/Microsoft.Authorization/locks")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			emptyLocks := armlocks.ManagementLockListResult{
				Value: []*armlocks.ManagementLockObject{},
			}
			return mocks.CreateHttpResponseWithBody(
				request, http.StatusOK, emptyLocks,
			)
		})

		// Tier 1 returns empty operations, Tier 2 falls through (no provision-param-hash
		// tag on the RG), so Tier 3 prompts the user per unknown resource group.
		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(
				options.Message, "Delete resource group 'RESOURCE_GROUP'?",
			)
		}).Respond(true)

		// After classification, an overall confirmation prompt fires for all owned RGs.
		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Delete 1 resource group(s)")
		}).Respond(true)

		infraProvider := createBicepProvider(t, mockContext)

		destroyOptions := provisioning.NewDestroyOptions(false, false)
		destroyResult, err := infraProvider.Destroy(*mockContext.Context, destroyOptions)

		require.Nil(t, err)
		require.NotNil(t, destroyResult)

		// Verify both prompts fired: Tier 3 per-RG + overall confirmation.
		consoleOutput := mockContext.Console.Output()
		require.Len(t, consoleOutput, 2)
		require.Contains(t, consoleOutput[0], "Delete resource group 'RESOURCE_GROUP'?")
		require.Contains(t, consoleOutput[1], "Delete 1 resource group(s)")
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

		// Verify console prompts — force+purge bypasses classification prompt and purge prompt.
		consoleOutput := mockContext.Console.Output()
		require.Len(t, consoleOutput, 0)
	})
}

func TestBicepDestroyLogAnalyticsWorkspace(t *testing.T) {
	t.Run("WithPurge", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		prepareBicepMocks(mockContext)
		prepareStateMocks(mockContext)
		prepareLogAnalyticsDestroyMocks(mockContext)

		infraProvider := createBicepProvider(t, mockContext)

		destroyOptions := provisioning.NewDestroyOptions(true, true)
		destroyResult, err := infraProvider.Destroy(*mockContext.Context, destroyOptions)

		require.NoError(t, err)
		require.NotNil(t, destroyResult)

		consoleOutput := mockContext.Console.Output()
		require.Len(t, consoleOutput, 0)
	})
}

// TestBicepDestroyClassifyAndDelete tests the classifyAndDeleteResourceGroups orchestrator,
// including force-bypass, Tier 1 classification, void-state lifecycle, and purge scoping.
func TestBicepDestroyClassifyAndDelete(t *testing.T) {
	// Helper: create a deployment operation targeting a resource group.
	makeRGOp := func(
		rgName string, opType armresources.ProvisioningOperation,
	) *armresources.DeploymentOperation {
		return &armresources.DeploymentOperation{
			OperationID: new("op-" + rgName),
			Properties: &armresources.DeploymentOperationProperties{
				ProvisioningOperation: new(opType),
				TargetResource: &armresources.TargetResource{
					ResourceType: new("Microsoft.Resources/resourceGroups"),
					ResourceName: new(rgName),
				},
			},
		}
	}

	t.Run("ForceBypassesClassification", func(t *testing.T) {
		// When --force is set, classification is skipped entirely.
		// Both RGs should be deleted directly, and no operations should be fetched.
		mockContext := mocks.NewMockContext(context.Background())
		prepareBicepMocks(mockContext)

		tracker := prepareClassifyDestroyMocks(mockContext, classifyMockCfg{
			rgNames: []string{"rg-created", "rg-existing"},
			operations: []*armresources.DeploymentOperation{
				makeRGOp("rg-created", armresources.ProvisioningOperationCreate),
				makeRGOp("rg-existing", armresources.ProvisioningOperationRead),
			},
		})

		infraProvider := createBicepProvider(t, mockContext)

		destroyOptions := provisioning.NewDestroyOptions(true, false) // force=true, purge=false
		result, err := infraProvider.Destroy(*mockContext.Context, destroyOptions)

		require.NoError(t, err)
		require.NotNil(t, result)

		// Both RGs deleted — force bypasses classification entirely.
		assert.Equal(t, int32(1), tracker.rgDeletes["rg-created"].Load(),
			"rg-created should be deleted when force=true")
		assert.Equal(t, int32(1), tracker.rgDeletes["rg-existing"].Load(),
			"rg-existing should be deleted when force=true")

		// Deployment operations NOT fetched (force short-circuits before calling Operations()).
		assert.Equal(t, int32(0), tracker.operationsGETs.Load(),
			"operations should not be fetched when force=true")
	})

	t.Run("ClassificationFiltersDeletion", func(t *testing.T) {
		// Tier 1 classification: Create op -> owned (delete), Read op -> external (skip).
		mockContext := mocks.NewMockContext(context.Background())
		prepareBicepMocks(mockContext)

		tracker := prepareClassifyDestroyMocks(mockContext, classifyMockCfg{
			rgNames: []string{"rg-created", "rg-existing"},
			operations: []*armresources.DeploymentOperation{
				makeRGOp("rg-created", armresources.ProvisioningOperationCreate),
				makeRGOp("rg-existing", armresources.ProvisioningOperationRead),
			},
		})

		// Overall confirmation prompt fires for owned RGs.
		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Delete 1 resource group(s)")
		}).Respond(true)

		infraProvider := createBicepProvider(t, mockContext)

		destroyOptions := provisioning.NewDestroyOptions(false, false)
		result, err := infraProvider.Destroy(*mockContext.Context, destroyOptions)

		require.NoError(t, err)
		require.NotNil(t, result)

		// Only the Created RG should be deleted.
		assert.Equal(t, int32(1), tracker.rgDeletes["rg-created"].Load(),
			"rg-created (Create op) should be deleted")
		// Read RG should be skipped.
		assert.Equal(t, int32(0), tracker.rgDeletes["rg-existing"].Load(),
			"rg-existing (Read op) should be skipped")

		// Operations were fetched for classification.
		assert.Equal(t, int32(1), tracker.operationsGETs.Load())
	})

	t.Run("VoidStateCalledOnSuccess", func(t *testing.T) {
		// After successful classification + deletion, voidDeploymentState must be called.
		mockContext := mocks.NewMockContext(context.Background())
		prepareBicepMocks(mockContext)

		tracker := prepareClassifyDestroyMocks(mockContext, classifyMockCfg{
			rgNames: []string{"rg-created"},
			operations: []*armresources.DeploymentOperation{
				makeRGOp("rg-created", armresources.ProvisioningOperationCreate),
			},
		})

		// Overall confirmation prompt fires for owned RGs.
		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Delete 1 resource group(s)")
		}).Respond(true)

		infraProvider := createBicepProvider(t, mockContext)

		destroyOptions := provisioning.NewDestroyOptions(false, false)
		result, err := infraProvider.Destroy(*mockContext.Context, destroyOptions)

		require.NoError(t, err)
		require.NotNil(t, result)

		// Void state should be called exactly once after successful deletion.
		assert.Equal(t, int32(1), tracker.voidStatePUTs.Load(),
			"voidDeploymentState should be called after successful classification")
	})

	t.Run("VoidStateCalledWhenAllRGsSkipped", func(t *testing.T) {
		// Even when all RGs are classified as external (all skipped),
		// voidDeploymentState must still be called to maintain deployment state.
		// This was a bug: if zero owned RGs remained, void state was skipped.
		mockContext := mocks.NewMockContext(context.Background())
		prepareBicepMocks(mockContext)

		tracker := prepareClassifyDestroyMocks(mockContext, classifyMockCfg{
			rgNames: []string{"rg-ext-1", "rg-ext-2"},
			operations: []*armresources.DeploymentOperation{
				makeRGOp("rg-ext-1", armresources.ProvisioningOperationRead),
				makeRGOp("rg-ext-2", armresources.ProvisioningOperationRead),
			},
		})

		infraProvider := createBicepProvider(t, mockContext)

		destroyOptions := provisioning.NewDestroyOptions(false, false)
		result, err := infraProvider.Destroy(*mockContext.Context, destroyOptions)

		require.NoError(t, err)
		require.NotNil(t, result)

		// Zero RGs deleted (all external).
		assert.Equal(t, int32(0), tracker.rgDeletes["rg-ext-1"].Load())
		assert.Equal(t, int32(0), tracker.rgDeletes["rg-ext-2"].Load())

		// Void state STILL called even though no RGs were deleted.
		assert.Equal(t, int32(1), tracker.voidStatePUTs.Load(),
			"voidDeploymentState should be called even when all RGs are skipped")
	})

	t.Run("PurgeTargetsScopedToOwnedRGs", func(t *testing.T) {
		// Purge targets (KeyVaults, etc.) should only be collected from
		// owned (deleted) RGs, not from skipped (external) RGs.
		// kv-ext is intentionally NOT mocked — if the code incorrectly
		// includes it in the purge set, the mock framework panics.
		mockContext := mocks.NewMockContext(context.Background())
		prepareBicepMocks(mockContext)

		tracker := prepareClassifyDestroyMocks(mockContext, classifyMockCfg{
			rgNames: []string{"rg-created", "rg-existing"},
			operations: []*armresources.DeploymentOperation{
				makeRGOp("rg-created", armresources.ProvisioningOperationCreate),
				makeRGOp("rg-existing", armresources.ProvisioningOperationRead),
			},
			withPurgeResources: true, // adds a KeyVault to each RG
		})

		// Overall confirmation prompt fires for owned RGs.
		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Delete 1 resource group(s)")
		}).Respond(true)

		infraProvider := createBicepProvider(t, mockContext)

		destroyOptions := provisioning.NewDestroyOptions(false, true) // purge=true
		result, err := infraProvider.Destroy(*mockContext.Context, destroyOptions)

		require.NoError(t, err)
		require.NotNil(t, result)

		// Only the owned RG's KeyVault should be inspected for purge properties.
		assert.Equal(t, int32(1), tracker.kvGETs["kv-owned"].Load(),
			"owned RG's KeyVault should be inspected for purge properties")

		// Owned RG's KeyVault should be purged (soft-delete enabled, purge protection off).
		assert.Equal(t, int32(1), tracker.kvPurges["kv-owned"].Load(),
			"owned RG's KeyVault should be purged")
	})

	t.Run("UserCancelPreservesDeploymentState", func(t *testing.T) {
		// When user declines the "Delete N resource group(s)?" confirmation,
		// voidDeploymentState must NOT be called and env keys must NOT be invalidated.
		// Regression test for: cancel returned nil error, causing state mutation on abort.
		mockContext := mocks.NewMockContext(context.Background())
		prepareBicepMocks(mockContext)

		tracker := prepareClassifyDestroyMocks(mockContext, classifyMockCfg{
			rgNames: []string{"rg-created"},
			operations: []*armresources.DeploymentOperation{
				makeRGOp("rg-created", armresources.ProvisioningOperationCreate),
			},
		})

		// User declines the overall confirmation prompt.
		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Delete 1 resource group(s)")
		}).Respond(false)

		infraProvider := createBicepProvider(t, mockContext)

		destroyOptions := provisioning.NewDestroyOptions(false, false)
		result, err := infraProvider.Destroy(*mockContext.Context, destroyOptions)

		require.Error(t, err, "user cancellation should return an error")
		require.ErrorIs(t, err, errUserCancelled)
		require.NotNil(t, result)

		// No RGs should be deleted — user cancelled.
		assert.Equal(t, int32(0), tracker.rgDeletes["rg-created"].Load(),
			"rg-created should NOT be deleted when user cancels")

		// Void state should NOT be called — user cancelled.
		assert.Equal(t, int32(0), tracker.voidStatePUTs.Load(),
			"voidDeploymentState should NOT be called when user cancels confirmation")

		// Env keys should not be invalidated — DestroyResult should be empty.
		assert.Empty(t, result.InvalidatedEnvKeys,
			"env keys should NOT be invalidated when user cancels")
	})
}

func TestDeploymentForResourceGroup(t *testing.T) {
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
		prompt.NewDefaultPrompter(env, mockContext.Console, accountManager, resourceService, cloud.AzurePublic()),
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
			Tags:     map[string]*string{"azd-env-name": new("test-env")},
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

	// Tier 2 tag check: GET individual resource group by name.
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.HasSuffix(request.URL.Path, "subscriptions/SUBSCRIPTION_ID/resourcegroups/RESOURCE_GROUP")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, *resourceGroup)
	})

	// Get list of resources to delete
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/resources")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, resourceList)
	})

	// Tier 4 lock check: no management locks on the RG.
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(request.URL.Path, "providers/Microsoft.Authorization/locks")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		emptyLocks := armlocks.ManagementLockListResult{
			Value: []*armlocks.ManagementLockObject{},
		}
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, emptyLocks)
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

	// List deployment operations — empty list so Tier 1 falls through to Tier 3 prompt
	// (used only for the non-force Interactive test; force mode bypasses classification).
	operationsResult := armresources.DeploymentOperationsListResult{
		Value: []*armresources.DeploymentOperation{},
	}
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.HasSuffix(
				request.URL.Path,
				"/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/test-env/operations",
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, operationsResult)
	})

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

	// List deployment operations (Tier 1 classification data).
	operationsResultLA := armresources.DeploymentOperationsListResult{
		Value: []*armresources.DeploymentOperation{
			{
				OperationID: new("op-rg-create"),
				Properties: &armresources.DeploymentOperationProperties{
					ProvisioningOperation: to.Ptr(armresources.ProvisioningOperationCreate),
					TargetResource: &armresources.TargetResource{
						ResourceType: new("Microsoft.Resources/resourceGroups"),
						ResourceName: new("RESOURCE_GROUP"),
					},
				},
			},
		},
	}
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.HasSuffix(
				request.URL.Path,
				"/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/test-env/operations",
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, operationsResultLA)
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

// --- Multi-RG classification destroy test helpers ---

// classifyMockCfg configures a multi-RG destroy test scenario.
type classifyMockCfg struct {
	rgNames            []string                            // RG names referenced in the deployment
	operations         []*armresources.DeploymentOperation // Tier 1 classification operations
	withPurgeResources bool                                // adds a KeyVault to each RG for purge testing
}

// classifyCallTracker tracks HTTP calls made during classification integration tests.
type classifyCallTracker struct {
	rgDeletes      map[string]*atomic.Int32 // per-RG DELETE call counts
	voidStatePUTs  atomic.Int32             // void state PUT calls
	operationsGETs atomic.Int32             // deployment operations GET calls
	kvGETs         map[string]*atomic.Int32 // per-KeyVault GET calls (purge property inspection)
	kvPurges       map[string]*atomic.Int32 // per-KeyVault purge POST calls
}

// prepareClassifyDestroyMocks sets up HTTP mocks for multi-RG destroy + classification tests.
// It registers deployment state, per-RG resource listing, deployment operations, RG deletion,
// void state, and optionally KeyVault purge mocks. Returns a tracker for asserting call counts.
func prepareClassifyDestroyMocks(
	mockContext *mocks.MockContext,
	cfg classifyMockCfg,
) *classifyCallTracker {
	// Register SubscriptionCredentialProvider in the mock container so Tier 4
	// helpers (listResourceGroupLocks, listResourceGroupResourcesWithTags) can
	// resolve credentials. Without this, the fail-safe error handling vetoes all RGs.
	mockContext.Container.MustRegisterSingleton(
		func() account.SubscriptionCredentialProvider {
			return mockaccount.SubscriptionCredentialProviderFunc(
				func(_ context.Context, _ string) (azcore.TokenCredential, error) {
					return mockContext.Credentials, nil
				},
			)
		},
	)

	// Register ARM client options so Tier 4 helpers use the mock HTTP transport.
	mockContext.Container.MustRegisterSingleton(
		func() *arm.ClientOptions {
			return mockContext.ArmClientOptions
		},
	)

	tracker := &classifyCallTracker{
		rgDeletes: make(map[string]*atomic.Int32, len(cfg.rgNames)),
		kvGETs:    make(map[string]*atomic.Int32),
		kvPurges:  make(map[string]*atomic.Int32),
	}
	for _, rg := range cfg.rgNames {
		tracker.rgDeletes[rg] = &atomic.Int32{}
	}

	// --- Build multi-RG deployment with OutputResources referencing each RG ---
	outputResources := make([]*armresources.ResourceReference, len(cfg.rgNames))
	for i, rg := range cfg.rgNames {
		id := fmt.Sprintf("/subscriptions/SUBSCRIPTION_ID/resourceGroups/%s", rg)
		outputResources[i] = &armresources.ResourceReference{ID: &id}
	}

	deployment := armresources.DeploymentExtended{
		ID:       new("DEPLOYMENT_ID"),
		Name:     new("test-env"),
		Location: new("eastus2"),
		Tags:     map[string]*string{"azd-env-name": new("test-env")},
		Type:     new("Microsoft.Resources/deployments"),
		Properties: &armresources.DeploymentPropertiesExtended{
			Outputs: map[string]any{
				"WEBSITE_URL": map[string]any{"value": "http://myapp.azurewebsites.net", "type": "string"},
			},
			OutputResources:   outputResources,
			ProvisioningState: to.Ptr(armresources.ProvisioningStateSucceeded),
			Timestamp:         new(time.Now()),
		},
	}

	deployResultBytes, _ := json.Marshal(deployment)

	// GET single deployment (used by Resources(), VoidState(), and Get())
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

	// GET list deployments (used by CompletedDeployments)
	deploymentsPage := &armresources.DeploymentListResult{
		Value: []*armresources.DeploymentExtended{&deployment},
	}
	deploymentsPageBytes, _ := json.Marshal(deploymentsPage)

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.HasSuffix(
			request.URL.Path,
			"/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/",
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer(deploymentsPageBytes)),
		}, nil
	})

	// --- Per-RG resource listing ---
	// When withPurgeResources is true, the first RG gets "kv-owned" and the second gets "kv-ext".
	kvMapping := map[string]string{} // rgName -> kvName
	if cfg.withPurgeResources && len(cfg.rgNames) >= 2 {
		kvMapping[cfg.rgNames[0]] = "kv-owned"
		kvMapping[cfg.rgNames[1]] = "kv-ext"
	}

	for _, rgName := range cfg.rgNames {
		resources := []*armresources.GenericResourceExpanded{}

		if kvName, ok := kvMapping[rgName]; ok {
			kvID := fmt.Sprintf(
				"/subscriptions/SUBSCRIPTION_ID/resourceGroups/%s/providers/%s/%s",
				rgName, string(azapi.AzureResourceTypeKeyVault), kvName,
			)
			resources = append(resources, &armresources.GenericResourceExpanded{
				ID:       &kvID,
				Name:     new(kvName),
				Type:     new(string(azapi.AzureResourceTypeKeyVault)),
				Location: new("eastus2"),
				Tags:     map[string]*string{"azd-env-name": new("test-env")},
			})
		}

		resList := armresources.ResourceListResult{Value: resources}
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				strings.Contains(request.URL.Path, fmt.Sprintf("resourceGroups/%s/resources", rgName))
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			return mocks.CreateHttpResponseWithBody(request, http.StatusOK, resList)
		})
	}

	// --- Deployment operations (Tier 1 classification data) ---
	operationsResult := armresources.DeploymentOperationsListResult{
		Value: cfg.operations,
	}
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.HasSuffix(
				request.URL.Path,
				"/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/test-env/operations",
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		tracker.operationsGETs.Add(1)
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, operationsResult)
	})

	// --- Per-RG deletion mocks (tracked) ---
	for _, rgName := range cfg.rgNames {
		counter := tracker.rgDeletes[rgName]
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodDelete &&
				strings.HasSuffix(
					request.URL.Path,
					fmt.Sprintf("subscriptions/SUBSCRIPTION_ID/resourcegroups/%s", rgName),
				)
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			counter.Add(1)
			return httpRespondFn(request)
		})
	}

	// --- Tier 4 lock listing mocks (return empty locks for each RG) ---
	for _, rgName := range cfg.rgNames {
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				strings.Contains(
					request.URL.Path,
					fmt.Sprintf(
						"resourceGroups/%s/providers/Microsoft.Authorization/locks",
						rgName,
					),
				)
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			emptyLocks := armlocks.ManagementLockListResult{Value: []*armlocks.ManagementLockObject{}}
			return mocks.CreateHttpResponseWithBody(request, http.StatusOK, emptyLocks)
		})
	}

	// --- LRO polling endpoint ---
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(request.URL.String(), "url-to-poll.net")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(request, 204)
	})

	// --- Void state: PUT empty deployment (tracked) ---
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPut &&
			strings.Contains(
				request.URL.Path,
				"/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/",
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		tracker.voidStatePUTs.Add(1)
		result := &armresources.DeploymentsClientCreateOrUpdateAtSubscriptionScopeResponse{
			DeploymentExtended: armresources.DeploymentExtended{
				ID:       new("DEPLOYMENT_ID"),
				Name:     new("test-env"),
				Location: new("eastus2"),
				Tags:     map[string]*string{"azd-env-name": new("test-env")},
				Type:     new("Microsoft.Resources/deployments"),
				Properties: &armresources.DeploymentPropertiesExtended{
					ProvisioningState: to.Ptr(armresources.ProvisioningStateSucceeded),
					Timestamp:         new(time.Now()),
				},
			},
		}
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, result)
	})

	// --- KeyVault mocks (for purge scoping test) ---
	if cfg.withPurgeResources {
		// Only mock the owned RG's KeyVault (kv-owned).
		// kv-ext is intentionally NOT mocked — if the code incorrectly includes it
		// in the purge set, the mock framework panics (which fails the test).
		kvOwnedGetCounter := &atomic.Int32{}
		tracker.kvGETs["kv-owned"] = kvOwnedGetCounter

		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				strings.HasSuffix(request.URL.Path, "/vaults/kv-owned")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			kvOwnedGetCounter.Add(1)
			kvResponse := armkeyvault.VaultsClientGetResponse{
				Vault: armkeyvault.Vault{
					ID: new(fmt.Sprintf(
						"/subscriptions/SUBSCRIPTION_ID/resourceGroups/%s/providers/%s/kv-owned",
						cfg.rgNames[0], string(azapi.AzureResourceTypeKeyVault),
					)),
					Name:     new("kv-owned"),
					Location: new("eastus2"),
					Properties: &armkeyvault.VaultProperties{
						EnableSoftDelete:      new(true),
						EnablePurgeProtection: new(false),
					},
				},
			}
			kvBytes, _ := json.Marshal(kvResponse)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBuffer(kvBytes)),
			}, nil
		})

		// Purge mock for kv-owned (tracked)
		kvPurgeCounter := &atomic.Int32{}
		tracker.kvPurges["kv-owned"] = kvPurgeCounter
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodPost &&
				strings.HasSuffix(request.URL.Path, "deletedVaults/kv-owned/purge")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			kvPurgeCounter.Add(1)
			return httpRespondFn(request)
		})
	}

	return tracker
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
	mockContext := mocks.NewMockContext(context.Background())
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
		prompt.NewDefaultPrompter(env, mockContext.Console, nil, nil, cloud.AzurePublic()),
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
	mockContext := mocks.NewMockContext(context.Background())
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
		prompt.NewDefaultPrompter(env, mockContext.Console, accountManager, resourceService, cloud.AzurePublic()),
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
	mockContext := mocks.NewMockContext(context.Background())

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
	mockContext := mocks.NewMockContext(context.Background())

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
			mockContext := mocks.NewMockContext(context.Background())

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
	mockContext := mocks.NewMockContext(context.Background())

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
	mockContext := mocks.NewMockContext(context.Background())

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

// ---------------------------------------------------------------------------
// Coverage-gap tests for destroyViaDeploymentDelete, isDeploymentStacksEnabled,
// deleteRGList error accumulation, and ARM-wiring credential failures.
// ---------------------------------------------------------------------------

// enableDeploymentStacks enables the deployment.stacks alpha feature via environment
// variable for the duration of the test. Uses t.Setenv for automatic cleanup.
func enableDeploymentStacks(t *testing.T) {
	t.Setenv("AZD_ALPHA_ENABLE_DEPLOYMENT_STACKS", "true")
}

// TestBicepDestroyViaDeploymentStacks tests the deployment-stacks branch of
// Destroy(), covering destroyViaDeploymentDelete (previously 0%) and the
// isDeploymentStacksEnabled true-path (previously 75%).
func TestBicepDestroyViaDeploymentStacks(t *testing.T) {
	t.Run("SuccessNoPurge", func(t *testing.T) {
		// With deployment stacks enabled and no purge resources, the destroy flow
		// should call deployment.Delete() (which deletes each RG), void state,
		// and skip purge entirely.
		enableDeploymentStacks(t)
		mockContext := mocks.NewMockContext(context.Background())
		prepareBicepMocks(mockContext)

		tracker := prepareClassifyDestroyMocks(mockContext, classifyMockCfg{
			rgNames: []string{"rg-alpha", "rg-beta"},
			// Operations are NOT used in the deployment-stacks path (no classification),
			// but prepareClassifyDestroyMocks requires them for the mock setup.
			operations:         []*armresources.DeploymentOperation{},
			withPurgeResources: false,
		})

		infraProvider := createBicepProvider(t, mockContext)
		destroyOptions := provisioning.NewDestroyOptions(false, false) // force=false, purge=false
		result, err := infraProvider.Destroy(*mockContext.Context, destroyOptions)

		require.NoError(t, err)
		require.NotNil(t, result)

		// Both RGs deleted via deployment.Delete → DeleteSubscriptionDeployment.
		assert.Equal(t, int32(1), tracker.rgDeletes["rg-alpha"].Load(),
			"rg-alpha should be deleted via deployment.Delete")
		assert.Equal(t, int32(1), tracker.rgDeletes["rg-beta"].Load(),
			"rg-beta should be deleted via deployment.Delete")

		// Classification operations NOT fetched (deployment stacks bypasses classification).
		assert.Equal(t, int32(0), tracker.operationsGETs.Load(),
			"operations should not be fetched in deployment-stacks path")

		// Void state called once (inside DeleteSubscriptionDeployment).
		assert.Equal(t, int32(1), tracker.voidStatePUTs.Load(),
			"void state should be called once inside DeleteSubscriptionDeployment")
	})

	t.Run("SuccessWithPurge", func(t *testing.T) {
		// With deployment stacks enabled AND purge, the deployment-stacks path
		// deletes RGs, then collects and purges soft-delete resources from ALL RGs.
		enableDeploymentStacks(t)
		mockContext := mocks.NewMockContext(context.Background())
		prepareBicepMocks(mockContext)

		tracker := prepareClassifyDestroyMocks(mockContext, classifyMockCfg{
			rgNames:            []string{"rg-alpha", "rg-beta"},
			operations:         []*armresources.DeploymentOperation{},
			withPurgeResources: true,
		})

		// In the deployment-stacks path, ALL RGs are purged (not just owned ones).
		// prepareClassifyDestroyMocks intentionally omits the kv-ext mock (to catch
		// incorrect inclusion in the classification path). Add it here for stacks path.
		kvExtGetCounter := &atomic.Int32{}
		tracker.kvGETs["kv-ext"] = kvExtGetCounter
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				strings.HasSuffix(request.URL.Path, "/vaults/kv-ext")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			kvExtGetCounter.Add(1)
			kvResponse := armkeyvault.VaultsClientGetResponse{
				Vault: armkeyvault.Vault{
					ID: new(fmt.Sprintf(
						"/subscriptions/SUBSCRIPTION_ID/resourceGroups/rg-beta/providers/%s/kv-ext",
						string(azapi.AzureResourceTypeKeyVault),
					)),
					Name:     new("kv-ext"),
					Location: new("eastus2"),
					Properties: &armkeyvault.VaultProperties{
						EnableSoftDelete:      new(true),
						EnablePurgeProtection: new(false),
					},
				},
			}
			kvBytes, _ := json.Marshal(kvResponse)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBuffer(kvBytes)),
			}, nil
		})

		kvExtPurgeCounter := &atomic.Int32{}
		tracker.kvPurges["kv-ext"] = kvExtPurgeCounter
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodPost &&
				strings.HasSuffix(request.URL.Path, "deletedVaults/kv-ext/purge")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			kvExtPurgeCounter.Add(1)
			return httpRespondFn(request)
		})

		// The purge prompt: "Would you like to permanently delete these resources instead?"
		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "permanently delete")
		}).Respond(true)

		infraProvider := createBicepProvider(t, mockContext)
		destroyOptions := provisioning.NewDestroyOptions(false, true) // force=false, purge=true
		result, err := infraProvider.Destroy(*mockContext.Context, destroyOptions)

		require.NoError(t, err)
		require.NotNil(t, result)

		// Both RGs deleted.
		assert.Equal(t, int32(1), tracker.rgDeletes["rg-alpha"].Load())
		assert.Equal(t, int32(1), tracker.rgDeletes["rg-beta"].Load())

		// Both KeyVaults inspected and purged (deployment stacks purges ALL RGs).
		assert.Equal(t, int32(1), tracker.kvGETs["kv-owned"].Load(),
			"kv-owned should be inspected for purge in deployment-stacks path")
		assert.Equal(t, int32(1), tracker.kvPurges["kv-owned"].Load(),
			"kv-owned should be purged in deployment-stacks path")
		assert.Equal(t, int32(1), tracker.kvGETs["kv-ext"].Load(),
			"kv-ext should be inspected for purge in deployment-stacks path (ALL RGs)")
		assert.Equal(t, int32(1), tracker.kvPurges["kv-ext"].Load(),
			"kv-ext should be purged in deployment-stacks path (ALL RGs)")
	})

	t.Run("DeploymentDeleteFailure", func(t *testing.T) {
		// When deployment.Delete() fails (e.g., RG deletion returns HTTP 500),
		// destroyViaDeploymentDelete propagates the error and Destroy returns it.
		enableDeploymentStacks(t)
		mockContext := mocks.NewMockContext(context.Background())
		prepareBicepMocks(mockContext)

		// Register credential/ARM providers that prepareClassifyDestroyMocks normally sets up.
		mockContext.Container.MustRegisterSingleton(
			func() account.SubscriptionCredentialProvider {
				return mockaccount.SubscriptionCredentialProviderFunc(
					func(_ context.Context, _ string) (azcore.TokenCredential, error) {
						return mockContext.Credentials, nil
					},
				)
			},
		)
		mockContext.Container.MustRegisterSingleton(
			func() *arm.ClientOptions {
				return mockContext.ArmClientOptions
			},
		)

		// Build deployment referencing a single RG.
		rgName := "rg-fail"
		rgID := fmt.Sprintf("/subscriptions/SUBSCRIPTION_ID/resourceGroups/%s", rgName)
		deployment := armresources.DeploymentExtended{
			ID:       new("DEPLOYMENT_ID"),
			Name:     new("test-env"),
			Location: new("eastus2"),
			Tags:     map[string]*string{"azd-env-name": new("test-env")},
			Type:     new("Microsoft.Resources/deployments"),
			Properties: &armresources.DeploymentPropertiesExtended{
				Outputs: map[string]any{
					"WEBSITE_URL": map[string]any{"value": "http://myapp.azurewebsites.net", "type": "string"},
				},
				OutputResources:   []*armresources.ResourceReference{{ID: &rgID}},
				ProvisioningState: to.Ptr(armresources.ProvisioningStateSucceeded),
				Timestamp:         new(time.Now()),
			},
		}
		deployResultBytes, _ := json.Marshal(deployment)

		// GET single deployment
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

		// GET list deployments
		deploymentsPage := &armresources.DeploymentListResult{
			Value: []*armresources.DeploymentExtended{&deployment},
		}
		deploymentsPageBytes, _ := json.Marshal(deploymentsPage)
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet && strings.HasSuffix(
				request.URL.Path,
				"/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/",
			)
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBuffer(deploymentsPageBytes)),
			}, nil
		})

		// Per-RG resource listing: empty
		resList := armresources.ResourceListResult{Value: []*armresources.GenericResourceExpanded{}}
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				strings.Contains(request.URL.Path, fmt.Sprintf("resourceGroups/%s/resources", rgName))
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			return mocks.CreateHttpResponseWithBody(request, http.StatusOK, resList)
		})

		// DELETE RG returns 500 Internal Server Error.
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodDelete &&
				strings.HasSuffix(
					request.URL.Path,
					fmt.Sprintf("subscriptions/SUBSCRIPTION_ID/resourcegroups/%s", rgName),
				)
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			// Use 409 Conflict (non-retryable) to avoid SDK retry delays.
			return &http.Response{
				Request:    request,
				Header:     http.Header{},
				StatusCode: http.StatusConflict,
				Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"Conflict","message":"simulated failure"}}`)),
			}, nil
		})

		// LRO polling endpoint (needed for mock framework).
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				strings.Contains(request.URL.String(), "url-to-poll.net")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			return mocks.CreateEmptyHttpResponse(request, 204)
		})

		infraProvider := createBicepProvider(t, mockContext)
		destroyOptions := provisioning.NewDestroyOptions(false, false)
		result, err := infraProvider.Destroy(*mockContext.Context, destroyOptions)

		require.Error(t, err)
		require.Nil(t, result)
		assert.Contains(t, err.Error(), "error deleting Azure resources")
	})

	t.Run("ZeroResourcesStillDeletesStack", func(t *testing.T) {
		// When deployment stacks are enabled and zero resources are found
		// (e.g., after manual cleanup), the stack itself must still be deleted
		// via deployment.Delete(). Regression: previously the zero-resources
		// fast-path ran before the stacks check, causing a no-op VoidState
		// and leaving the stack/deny-assignments behind.
		enableDeploymentStacks(t)
		mockContext := mocks.NewMockContext(context.Background())
		prepareBicepMocks(mockContext)

		tracker := prepareClassifyDestroyMocks(mockContext, classifyMockCfg{
			rgNames:            []string{}, // zero resource groups
			operations:         []*armresources.DeploymentOperation{},
			withPurgeResources: false,
		})

		infraProvider := createBicepProvider(t, mockContext)
		destroyOptions := provisioning.NewDestroyOptions(false, false)
		result, err := infraProvider.Destroy(*mockContext.Context, destroyOptions)

		require.NoError(t, err)
		require.NotNil(t, result)

		// Void state called via deployment.Delete (inside DeleteSubscriptionDeployment).
		assert.Equal(t, int32(1), tracker.voidStatePUTs.Load(),
			"void state should be called via deployment.Delete even with zero resources")
	})
}

// TestBicepDestroyDeleteRGListPartialFailure tests that deleteRGList continues
// attempting remaining RGs when one delete fails, and returns a joined error
// containing all individual failures. This covers the error-accumulation loop
// at deleteRGList lines 175-183 (previously 65% coverage).
func TestBicepDestroyDeleteRGListPartialFailure(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	prepareBicepMocks(mockContext)

	// Register credential/ARM providers.
	mockContext.Container.MustRegisterSingleton(
		func() account.SubscriptionCredentialProvider {
			return mockaccount.SubscriptionCredentialProviderFunc(
				func(_ context.Context, _ string) (azcore.TokenCredential, error) {
					return mockContext.Credentials, nil
				},
			)
		},
	)
	mockContext.Container.MustRegisterSingleton(
		func() *arm.ClientOptions {
			return mockContext.ArmClientOptions
		},
	)

	rgNames := []string{"rg-ok", "rg-fail", "rg-ok2"}

	// Build deployment referencing three RGs.
	outputResources := make([]*armresources.ResourceReference, len(rgNames))
	for i, rg := range rgNames {
		id := fmt.Sprintf("/subscriptions/SUBSCRIPTION_ID/resourceGroups/%s", rg)
		outputResources[i] = &armresources.ResourceReference{ID: &id}
	}

	deployment := armresources.DeploymentExtended{
		ID:       new("DEPLOYMENT_ID"),
		Name:     new("test-env"),
		Location: new("eastus2"),
		Tags:     map[string]*string{"azd-env-name": new("test-env")},
		Type:     new("Microsoft.Resources/deployments"),
		Properties: &armresources.DeploymentPropertiesExtended{
			Outputs: map[string]any{
				"WEBSITE_URL": map[string]any{"value": "http://myapp.azurewebsites.net", "type": "string"},
			},
			OutputResources:   outputResources,
			ProvisioningState: to.Ptr(armresources.ProvisioningStateSucceeded),
			Timestamp:         new(time.Now()),
		},
	}
	deployResultBytes, _ := json.Marshal(deployment)

	// GET single deployment
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

	// GET list deployments
	deploymentsPage := &armresources.DeploymentListResult{
		Value: []*armresources.DeploymentExtended{&deployment},
	}
	deploymentsPageBytes, _ := json.Marshal(deploymentsPage)
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.HasSuffix(
			request.URL.Path,
			"/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/",
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer(deploymentsPageBytes)),
		}, nil
	})

	// Per-RG resource listing: empty
	for _, rgName := range rgNames {
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				strings.Contains(request.URL.Path, fmt.Sprintf("resourceGroups/%s/resources", rgName))
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			resList := armresources.ResourceListResult{Value: []*armresources.GenericResourceExpanded{}}
			return mocks.CreateHttpResponseWithBody(request, http.StatusOK, resList)
		})
	}

	// Deployment operations: all Create (so Tier 1 classifies all as owned).
	ops := make([]*armresources.DeploymentOperation, len(rgNames))
	for i, rg := range rgNames {
		ops[i] = &armresources.DeploymentOperation{
			OperationID: new("op-" + rg),
			Properties: &armresources.DeploymentOperationProperties{
				ProvisioningOperation: new(armresources.ProvisioningOperationCreate),
				TargetResource: &armresources.TargetResource{
					ResourceType: new("Microsoft.Resources/resourceGroups"),
					ResourceName: new(rg),
				},
			},
		}
	}
	operationsResult := armresources.DeploymentOperationsListResult{Value: ops}
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.HasSuffix(
				request.URL.Path,
				"/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/test-env/operations",
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, operationsResult)
	})

	// Tier 4 lock listing: no locks for each RG.
	for _, rgName := range rgNames {
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				strings.Contains(
					request.URL.Path,
					fmt.Sprintf("resourceGroups/%s/providers/Microsoft.Authorization/locks", rgName),
				)
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			emptyLocks := armlocks.ManagementLockListResult{Value: []*armlocks.ManagementLockObject{}}
			return mocks.CreateHttpResponseWithBody(request, http.StatusOK, emptyLocks)
		})
	}

	// DELETE mocks: rg-ok and rg-ok2 succeed, rg-fail returns HTTP 500.
	rgDeleteCounts := map[string]*atomic.Int32{
		"rg-ok":   {},
		"rg-fail": {},
		"rg-ok2":  {},
	}

	for _, rg := range rgNames {
		counter := rgDeleteCounts[rg]
		failRG := rg == "rg-fail"
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodDelete &&
				strings.HasSuffix(
					request.URL.Path,
					fmt.Sprintf("subscriptions/SUBSCRIPTION_ID/resourcegroups/%s", rg),
				)
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			counter.Add(1)
			if failRG {
				// Use 409 Conflict (non-retryable) to avoid SDK retry noise.
				return &http.Response{
					Request:    request,
					Header:     http.Header{},
					StatusCode: http.StatusConflict,
					Body: io.NopCloser(strings.NewReader(
						`{"error":{"code":"Conflict","message":"simulated RG delete failure"}}`,
					)),
				}, nil
			}
			return httpRespondFn(request)
		})
	}

	// LRO polling endpoint.
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(request.URL.String(), "url-to-poll.net")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(request, 204)
	})

	// Void state PUT.
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPut &&
			strings.Contains(
				request.URL.Path,
				"/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/",
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		voidResult := &armresources.DeploymentsClientCreateOrUpdateAtSubscriptionScopeResponse{
			DeploymentExtended: armresources.DeploymentExtended{
				ID:       new("DEPLOYMENT_ID"),
				Name:     new("test-env"),
				Location: new("eastus2"),
				Tags:     map[string]*string{"azd-env-name": new("test-env")},
				Type:     new("Microsoft.Resources/deployments"),
				Properties: &armresources.DeploymentPropertiesExtended{
					ProvisioningState: to.Ptr(armresources.ProvisioningStateSucceeded),
					Timestamp:         new(time.Now()),
				},
			},
		}
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, voidResult)
	})

	// Overall confirmation prompt for classification (force=true bypasses this,
	// but we use force=true here to bypass prompt).
	infraProvider := createBicepProvider(t, mockContext)
	destroyOptions := provisioning.NewDestroyOptions(true, false) // force=true, purge=false
	result, err := infraProvider.Destroy(*mockContext.Context, destroyOptions)

	// The partial failure in deleteRGList should propagate as an error.
	require.Error(t, err)
	require.Nil(t, result)
	assert.Contains(t, err.Error(), "rg-fail",
		"error should mention the failed resource group")

	// Verify ALL RGs were attempted (deleteRGList doesn't stop on first failure).
	assert.Equal(t, int32(1), rgDeleteCounts["rg-ok"].Load(),
		"rg-ok should be attempted")
	assert.Equal(t, int32(1), rgDeleteCounts["rg-fail"].Load(),
		"rg-fail should be attempted")
	assert.Equal(t, int32(1), rgDeleteCounts["rg-ok2"].Load(),
		"rg-ok2 should still be attempted after rg-fail fails")
}

// TestBicepDestroyCredentialResolutionFailure tests that when the credential
// provider is NOT registered in the container, the ARM wiring fails gracefully
// for getResourceGroupTags (returns nil,nil → Tier 2 falls through) and
// listResourceGroupLocks (returns error → fail-safe veto).
// This covers the credential-failure branches in getResourceGroupTags (61%)
// and listResourceGroupLocks (48%).
func TestBicepDestroyCredentialResolutionFailure(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	prepareBicepMocks(mockContext)

	// Intentionally do NOT register SubscriptionCredentialProvider or arm.ClientOptions.
	// This causes getResourceGroupTags and listResourceGroupLocks to fail on credential resolution.

	rgNames := []string{"rg-alpha"}

	// Build deployment referencing one RG.
	rgID := fmt.Sprintf("/subscriptions/SUBSCRIPTION_ID/resourceGroups/%s", rgNames[0])
	deployment := armresources.DeploymentExtended{
		ID:       new("DEPLOYMENT_ID"),
		Name:     new("test-env"),
		Location: new("eastus2"),
		Tags:     map[string]*string{"azd-env-name": new("test-env")},
		Type:     new("Microsoft.Resources/deployments"),
		Properties: &armresources.DeploymentPropertiesExtended{
			Outputs: map[string]any{
				"WEBSITE_URL": map[string]any{"value": "http://myapp.azurewebsites.net", "type": "string"},
			},
			OutputResources:   []*armresources.ResourceReference{{ID: &rgID}},
			ProvisioningState: to.Ptr(armresources.ProvisioningStateSucceeded),
			Timestamp:         new(time.Now()),
		},
	}
	deployResultBytes, _ := json.Marshal(deployment)

	// GET single deployment
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

	// GET list deployments
	deploymentsPage := &armresources.DeploymentListResult{
		Value: []*armresources.DeploymentExtended{&deployment},
	}
	deploymentsPageBytes, _ := json.Marshal(deploymentsPage)
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.HasSuffix(
			request.URL.Path,
			"/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/",
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer(deploymentsPageBytes)),
		}, nil
	})

	// Per-RG resource listing: empty
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(request.URL.Path, fmt.Sprintf("resourceGroups/%s/resources", rgNames[0]))
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		resList := armresources.ResourceListResult{Value: []*armresources.GenericResourceExpanded{}}
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, resList)
	})

	// Deployment operations: Create (so Tier 1 classifies as owned).
	ops := []*armresources.DeploymentOperation{
		{
			OperationID: new("op-rg-alpha"),
			Properties: &armresources.DeploymentOperationProperties{
				ProvisioningOperation: new(armresources.ProvisioningOperationCreate),
				TargetResource: &armresources.TargetResource{
					ResourceType: new("Microsoft.Resources/resourceGroups"),
					ResourceName: new(rgNames[0]),
				},
			},
		},
	}
	operationsResult := armresources.DeploymentOperationsListResult{Value: ops}
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.HasSuffix(
				request.URL.Path,
				"/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/test-env/operations",
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, operationsResult)
	})

	// LRO polling endpoint.
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(request.URL.String(), "url-to-poll.net")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(request, 204)
	})

	// Void state PUT (after classification completes with all RGs skipped).
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPut &&
			strings.Contains(
				request.URL.Path,
				"/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/",
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		voidResult := &armresources.DeploymentsClientCreateOrUpdateAtSubscriptionScopeResponse{
			DeploymentExtended: armresources.DeploymentExtended{
				ID:       new("DEPLOYMENT_ID"),
				Name:     new("test-env"),
				Location: new("eastus2"),
				Tags:     map[string]*string{"azd-env-name": new("test-env")},
				Type:     new("Microsoft.Resources/deployments"),
				Properties: &armresources.DeploymentPropertiesExtended{
					ProvisioningState: to.Ptr(armresources.ProvisioningStateSucceeded),
					Timestamp:         new(time.Now()),
				},
			},
		}
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, voidResult)
	})

	infraProvider := createBicepProvider(t, mockContext)
	destroyOptions := provisioning.NewDestroyOptions(false, false) // force=false, purge=false
	result, err := infraProvider.Destroy(*mockContext.Context, destroyOptions)

	// Tier 4 listResourceGroupLocks fails on credential resolution.
	// fail-safe behavior vetoes all RGs → classifyAndDeleteResourceGroups reports
	// classification error because all RGs are vetoed with no owned RGs to delete.
	// The exact error depends on whether the veto causes an empty "owned" list
	// (which results in skipping deletion) or propagates as a classify error.
	//
	// In either case, the credential failure path in listResourceGroupLocks IS exercised,
	// covering the gap at lines 261-267 and 275-278 of bicep_destroy.go.
	// The actual behavior: listResourceGroupLocks error → fail-safe veto → RG not deleted.
	// Since ALL RGs are vetoed, classifyAndDeleteResourceGroups returns (nil, skipped, nil).
	// Then voidDeploymentState runs (no classify error), so Destroy succeeds.
	require.NoError(t, err)
	require.NotNil(t, result)
}
