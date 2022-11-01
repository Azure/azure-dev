package infra

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
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
	tmpPath := t.TempDir()
	parametersPath := path.Join(tmpPath, "params.json")
	createTmpFile := os.WriteFile(parametersPath, []byte(testArmParametersFile), osutil.PermissionFile)
	require.NoError(t, createTmpFile)

	t.Run("SubscriptionScopeSuccess", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodPut && strings.Contains(
				request.URL.Path,
				"/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/DEPLOYMENT_NAME",
			)
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body:       io.NopCloser(bytes.NewBuffer([]byte(testArmResponse))),
				Request: &http.Request{
					Method: http.MethodGet,
				},
			}, nil
		})

		scope := NewSubscriptionScope(*mockContext.Context, "eastus2", "SUBSCRIPTION_ID", "DEPLOYMENT_NAME")

		armTemplate := azure.ArmTemplate(testArmTemplate)
		err := scope.Deploy(*mockContext.Context, &armTemplate, parametersPath)
		require.NoError(t, err)
	})

	t.Run("ResourceGroupScopeSuccess", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodPut && strings.Contains(
				request.URL.Path,
				"/subscriptions/SUBSCRIPTION_ID/resourcegroups/RESOURCE_GROUP/providers/"+
					"Microsoft.Resources/deployments/DEPLOYMENT_NAME",
			)
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBuffer([]byte(testArmResponse))),
				Request: &http.Request{
					Method: http.MethodGet,
				},
			}, nil
		})

		scope := NewResourceGroupScope(*mockContext.Context, "SUBSCRIPTION_ID", "RESOURCE_GROUP", "DEPLOYMENT_NAME")

		armTemplate := azure.ArmTemplate(testArmTemplate)
		err := scope.Deploy(*mockContext.Context, &armTemplate, parametersPath)
		require.NoError(t, err)
	})
}

func TestScopeGetResourceOperations(t *testing.T) {
	t.Run("SubscriptionScopeSuccess", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet && strings.Contains(
				request.URL.Path,
				"/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/DEPLOYMENT_NAME/operations",
			)
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBuffer([]byte(deploymentBytes))),
				Request: &http.Request{
					Method: http.MethodGet,
				},
			}, nil
		})

		scope := NewSubscriptionScope(*mockContext.Context, "eastus2", "SUBSCRIPTION_ID", "DEPLOYMENT_NAME")

		operations, err := scope.GetResourceOperations(*mockContext.Context)
		require.NoError(t, err)
		require.Len(t, operations, 1)
	})

	t.Run("ResourceGroupScopeSuccess", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet && strings.Contains(
				request.URL.Path,
				"/subscriptions/SUBSCRIPTION_ID/resourcegroups/RESOURCE_GROUP/deployments/DEPLOYMENT_NAME/operations",
			)
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBuffer([]byte(deploymentBytes))),
				Request: &http.Request{
					Method: http.MethodGet,
				},
			}, nil
		})
		scope := NewResourceGroupScope(*mockContext.Context, "SUBSCRIPTION_ID", "RESOURCE_GROUP", "DEPLOYMENT_NAME")

		operations, err := scope.GetResourceOperations(*mockContext.Context)
		require.NoError(t, err)
		require.Len(t, operations, 1)
	})
}

var deploymentBytes string = `{
	"nextLink": "",
	"value": [{
		"id": "id",
		"operationId": "foo",
		"properties": {
		}
	}]	
}`

var testArmResponse string = `{
	"id":"/subscriptions/faa080af-c1d8-40ad-9cce-e1a450ca5b57/providers/Microsoft.Resources/deployments/foo",
	"name":"foo",
	"type":"Microsoft.Resources/deployments",
	"location":"westus3",
	"properties":{
		"templateHash":"10006264233799735596",
		"parameters":{
			"environmentName":{"type":"String","value":"foo"},
			"location":{"type":"String","value":"westus3"}
		}
	}
}
`

var testArmParametersFile string = `{
	"parameters": {
		"location": {
			"value": "West US"
		}
	}
}`

var testArmTemplate string = `{
"$schema": "https://schema.management.azure.com/schemas/2015-01-01/deploymentTemplate.json#",
"contentVersion": "1.0.0.0",
"parameters": {
	"location": {
	"type": "string",
	"allowedValues": [
		"East US"
	],
	"metadata": {
		"description": "Location to deploy to"
	}
	}
},
"resources": [
	{
	"type": "Microsoft.Compute/availabilitySets",
	"name": "availabilitySet1",
	"apiVersion": "2019-07-01",
	"location": "[parameters('location')]",
	"properties": {}
	}
],
"outputs": {
	"parameter": {
	"type": "object",
	"value": "[reference('Microsoft.Compute/availabilitySets/availabilitySet1')]"
	}
}}`
