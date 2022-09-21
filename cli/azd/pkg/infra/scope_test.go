package infra

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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

	deployment := azcli.AzCliDeploymentResult{
		Properties: azcli.AzCliDeploymentResultProperties{
			Outputs: outputs,
		},
	}
	deploymentBytes, _ := json.Marshal(deployment)

	t.Run("SubscriptionScopeSuccess", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "az deployment sub show")
		}).Respond(exec.NewRunResult(0, string(deploymentBytes), ""))

		scope := NewSubscriptionScope(*mockContext.Context, "eastus2", "SUBSCRIPTION_ID", "DEPLOYMENT_NAME")

		deployment, err := scope.GetDeployment(*mockContext.Context)
		require.NoError(t, err)
		require.Equal(t, outputs["APP_URL"].Value, deployment.Properties.Outputs["APP_URL"].Value)
	})

	t.Run("ResourceGroupScopeSuccess", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "az deployment group show")
		}).Respond(exec.NewRunResult(0, string(deploymentBytes), ""))

		scope := NewResourceGroupScope(*mockContext.Context, "SUBSCRIPTION_ID", "RESOURCE_GROUP", "DEPLOYMENT_NAME")

		deployment, err := scope.GetDeployment(*mockContext.Context)
		require.NoError(t, err)
		require.Equal(t, outputs["APP_URL"].Value, deployment.Properties.Outputs["APP_URL"].Value)
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
