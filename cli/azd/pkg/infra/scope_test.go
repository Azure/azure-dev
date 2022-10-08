package infra

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestScopeGetDeployment(t *testing.T) {
	outputs := make(map[string]azcli.AzCliDeploymentOutput)
	outputs["APP_URL"] = azcli.AzCliDeploymentOutput{
		Type:  "string",
		Value: "https://www.myapp.com",
	}

	// mocked response for get deployment from subscription
	deploymentWithOptions := &armresources.DeploymentsClientGetAtSubscriptionScopeResponse{
		DeploymentExtended: armresources.DeploymentExtended{
			Properties: &armresources.DeploymentPropertiesExtended{
				Outputs: outputs,
			},
		},
	}
	deploymentResourceGroupWithOptions := &armresources.DeploymentsClientGetResponse{
		DeploymentExtended: armresources.DeploymentExtended{
			Properties: &armresources.DeploymentPropertiesExtended{
				Outputs: outputs,
			},
		},
	}

	t.Run("SubscriptionScopeSuccess", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.MockDeployments.WhenGetAtSubscriptionScope(deploymentWithOptions)

		scope := NewSubscriptionScope(*mockContext.Context, "eastus2", "SUBSCRIPTION_ID", "DEPLOYMENT_NAME")

		deployment, err := scope.GetDeployment(*mockContext.Context)
		require.NoError(t, err)
		responseOutputs := deployment.Properties.Outputs.(map[string]azcli.AzCliDeploymentOutput)["APP_URL"]
		require.Equal(t, outputs["APP_URL"].Value, responseOutputs.Value)
		require.Equal(t, outputs["APP_URL"].Type, responseOutputs.Type)
	})

	t.Run("ResourceGroupScopeSuccess", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.MockDeployments.WhenGetResourceGroupDeployment(deploymentResourceGroupWithOptions)

		scope := NewResourceGroupScope(*mockContext.Context, "SUBSCRIPTION_ID", "RESOURCE_GROUP", "DEPLOYMENT_NAME")

		deployment, err := scope.GetDeployment(*mockContext.Context)
		require.NoError(t, err)
		responseOutputs := deployment.Properties.Outputs.(map[string]azcli.AzCliDeploymentOutput)["APP_URL"]
		require.Equal(t, outputs["APP_URL"].Value, responseOutputs.Value)
		require.Equal(t, outputs["APP_URL"].Type, responseOutputs.Type)
	})
}

func TestScopeDeploy(t *testing.T) {
	deployment := azcli.AzCliDeploymentResult{}
	deploymentBytes, _ := json.Marshal(deployment)

	t.Run("SubscriptionScopeSuccess", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "az deployment sub create")
		}).Respond(exec.NewRunResult(0, string(deploymentBytes), ""))

		scope := NewSubscriptionScope(*mockContext.Context, "eastus2", "SUBSCRIPTION_ID", "DEPLOYMENT_NAME")

		err := scope.Deploy(*mockContext.Context, "/path/to/template.bicep", "path/to/params.json")
		require.NoError(t, err)
	})

	t.Run("ResourceGroupScopeSuccess", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "az deployment group create")
		}).Respond(exec.NewRunResult(0, string(deploymentBytes), ""))

		scope := NewResourceGroupScope(*mockContext.Context, "SUBSCRIPTION_ID", "RESOURCE_GROUP", "DEPLOYMENT_NAME")

		err := scope.Deploy(*mockContext.Context, "/path/to/template.bicep", "path/to/params.json")
		require.NoError(t, err)
	})
}

func TestScopeGetResourceOperations(t *testing.T) {
	operations := []azcli.AzCliResourceOperation{}
	deploymentBytes, _ := json.Marshal(operations)

	t.Run("SubscriptionScopeSuccess", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "az deployment operation sub list")
		}).Respond(exec.NewRunResult(0, string(deploymentBytes), ""))

		scope := NewSubscriptionScope(*mockContext.Context, "eastus2", "SUBSCRIPTION_ID", "DEPLOYMENT_NAME")

		operations, err := scope.GetResourceOperations(*mockContext.Context)
		require.NoError(t, err)
		require.Len(t, operations, 0)
	})

	t.Run("ResourceGroupScopeSuccess", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "az deployment operation group list")
		}).Respond(exec.NewRunResult(0, string(deploymentBytes), ""))

		scope := NewResourceGroupScope(*mockContext.Context, "SUBSCRIPTION_ID", "RESOURCE_GROUP", "DEPLOYMENT_NAME")

		operations, err := scope.GetResourceOperations(*mockContext.Context)
		require.NoError(t, err)
		require.Len(t, operations, 0)
	})
}
