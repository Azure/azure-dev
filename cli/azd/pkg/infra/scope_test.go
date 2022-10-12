package infra

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

		subscriptionId := "SUBSCRIPTION_ID"
		deploymentName := "DEPLOYMENT_NAME"

		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet && strings.Contains(
				request.URL.Path,
				fmt.Sprintf(
					"/subscriptions/%s/providers/Microsoft.Resources/deployments/%s",
					subscriptionId,
					deploymentName),
			)
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			subscriptionsListBytes, _ := json.Marshal(deploymentWithOptions)

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBuffer(subscriptionsListBytes)),
			}, nil
		})

		scope := NewSubscriptionScope(*mockContext.Context, "eastus2", subscriptionId, deploymentName)

		deployment, err := scope.GetDeployment(*mockContext.Context)
		require.NoError(t, err)
		responseOutputs := deployment.Properties.Outputs.(map[string]interface{})["APP_URL"].(map[string]interface{})
		require.Equal(t, outputs["APP_URL"].Value, responseOutputs["value"].(string))
		require.Equal(t, outputs["APP_URL"].Type, responseOutputs["type"].(string))
	})

	t.Run("ResourceGroupScopeSuccess", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		subscriptionId := "SUBSCRIPTION_ID"
		deploymentName := "DEPLOYMENT_NAME"
		resourceGroupName := "RESOURCE_GROUP"

		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet && strings.Contains(
				request.URL.Path,
				fmt.Sprintf(
					"/subscriptions/%s/resourcegroups/%s/providers/Microsoft.Resources/deployments/%s",
					subscriptionId,
					resourceGroupName,
					deploymentName),
			)
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			subscriptionsListBytes, _ := json.Marshal(deploymentResourceGroupWithOptions)

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBuffer(subscriptionsListBytes)),
			}, nil
		})

		scope := NewResourceGroupScope(*mockContext.Context, subscriptionId, resourceGroupName, deploymentName)

		deployment, err := scope.GetDeployment(*mockContext.Context)
		require.NoError(t, err)
		responseOutputs := deployment.Properties.Outputs.(map[string]interface{})["APP_URL"].(map[string]interface{})
		require.Equal(t, outputs["APP_URL"].Value, responseOutputs["value"].(string))
		require.Equal(t, outputs["APP_URL"].Type, responseOutputs["type"].(string))
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
