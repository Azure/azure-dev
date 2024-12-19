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
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazapi"
	"github.com/stretchr/testify/require"
)

func TestScopeGetDeployment(t *testing.T) {
	outputs := make(map[string]azapi.AzCliDeploymentOutput)
	outputs["APP_URL"] = azapi.AzCliDeploymentOutput{
		Type:  "string",
		Value: "https://www.myapp.com",
	}

	// mocked response for get deployment from subscription
	deploymentWithOptions := &armresources.DeploymentsClientGetAtSubscriptionScopeResponse{
		DeploymentExtended: armresources.DeploymentExtended{
			ID: to.Ptr(
				"/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/DEPLOYMENT_NAME",
			),
			Location: to.Ptr("eastus2"),
			Type:     to.Ptr("Microsoft.Resources/deployments"),
			Tags:     map[string]*string{},
			Name:     to.Ptr("DEPLOYMENT_NAME"),
			Properties: &armresources.DeploymentPropertiesExtended{
				ProvisioningState: to.Ptr(armresources.ProvisioningStateCreated),
				Outputs:           outputs,
				Timestamp:         to.Ptr(time.Now().UTC()),
			},
		},
	}
	deploymentResourceGroupWithOptions := &armresources.DeploymentsClientGetResponse{
		DeploymentExtended: armresources.DeploymentExtended{
			ID: to.Ptr(
				"/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/DEPLOYMENT_NAME",
			),
			Location: to.Ptr("eastus2"),
			Type:     to.Ptr("Microsoft.Resources/deployments"),
			Tags:     map[string]*string{},
			Name:     to.Ptr("DEPLOYMENT_NAME"),
			Properties: &armresources.DeploymentPropertiesExtended{
				ProvisioningState: to.Ptr(armresources.ProvisioningStateCreated),
				Outputs:           outputs,
				Timestamp:         to.Ptr(time.Now().UTC()),
			},
		},
	}

	t.Run("SubscriptionScopeSuccess", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		deploymentService := mockazapi.NewDeploymentsServiceFromMockContext(mockContext)

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

		scope := newSubscriptionScope(deploymentService, subscriptionId, "eastus2")
		target := NewSubscriptionDeployment(
			scope,
			deploymentName,
		)

		deployment, err := target.Get(*mockContext.Context)
		require.NoError(t, err)
		responseOutputs := deployment.Outputs.(map[string]interface{})["APP_URL"].(map[string]interface{})
		require.Equal(t, outputs["APP_URL"].Value, responseOutputs["value"].(string))
		require.Equal(t, outputs["APP_URL"].Type, responseOutputs["type"].(string))
	})

	t.Run("ResourceGroupScopeSuccess", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		deploymentService := mockazapi.NewDeploymentsServiceFromMockContext(mockContext)

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

		scope := newResourceGroupScope(deploymentService, subscriptionId, resourceGroupName)
		target := NewResourceGroupDeployment(
			scope,
			deploymentName,
		)

		deployment, err := target.Get(*mockContext.Context)
		require.NoError(t, err)
		responseOutputs := deployment.Outputs.(map[string]interface{})["APP_URL"].(map[string]interface{})
		require.Equal(t, outputs["APP_URL"].Value, responseOutputs["value"].(string))
		require.Equal(t, outputs["APP_URL"].Type, responseOutputs["type"].(string))
	})
}

var deploymentExtended = armresources.DeploymentExtended{
	ID:       to.Ptr("/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/DEPLOYMENT_NAME"),
	Location: to.Ptr("eastus2"),
	Type:     to.Ptr("Microsoft.Resources/deployments"),
	Tags:     map[string]*string{},
	Name:     to.Ptr("DEPLOYMENT_NAME"),
	Properties: &armresources.DeploymentPropertiesExtended{
		ProvisioningState: to.Ptr(armresources.ProvisioningStateSucceeded),
		Timestamp:         to.Ptr(time.Now().UTC()),
	},
}

func TestScopeDeploy(t *testing.T) {
	t.Run("SubscriptionScopeSuccess", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		deploymentService := mockazapi.NewDeploymentsServiceFromMockContext(mockContext)

		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodPut && strings.Contains(
				request.URL.Path,
				"/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/DEPLOYMENT_NAME",
			)
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			return mocks.CreateHttpResponseWithBody(
				request,
				http.StatusCreated,
				armresources.DeploymentsClientCreateOrUpdateAtSubscriptionScopeResponse{
					DeploymentExtended: deploymentExtended,
				},
			)
		})

		scope := newSubscriptionScope(deploymentService, "SUBSCRIPTION_ID", "eastus2")
		target := NewSubscriptionDeployment(
			scope,
			"DEPLOYMENT_NAME",
		)

		armTemplate := azure.RawArmTemplate(testArmTemplate)
		_, err := target.Deploy(*mockContext.Context, armTemplate, testArmParameters, nil, nil)
		require.NoError(t, err)
	})

	t.Run("ResourceGroupScopeSuccess", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		deploymentService := mockazapi.NewDeploymentsServiceFromMockContext(mockContext)

		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodPut && strings.Contains(
				request.URL.Path,
				//nolint:lll
				"/subscriptions/SUBSCRIPTION_ID/resourcegroups/RESOURCE_GROUP/providers/Microsoft.Resources/deployments/DEPLOYMENT_NAME",
			)
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			return mocks.CreateHttpResponseWithBody(
				request,
				http.StatusCreated,
				armresources.DeploymentsClientCreateOrUpdateResponse{
					DeploymentExtended: deploymentExtended,
				},
			)
		})

		scope := newResourceGroupScope(deploymentService, "SUBSCRIPTION_ID", "RESOURCE_GROUP")
		target := NewResourceGroupDeployment(
			scope,
			"DEPLOYMENT_NAME",
		)

		armTemplate := azure.RawArmTemplate(testArmTemplate)
		_, err := target.Deploy(*mockContext.Context, armTemplate, testArmParameters, nil, nil)
		require.NoError(t, err)
	})
}

var deploymentOperationsListResult = armresources.DeploymentOperationsListResult{
	Value: []*armresources.DeploymentOperation{
		{
			ID:          to.Ptr("operation-1"),
			OperationID: to.Ptr("operation-1"),
			Properties: &armresources.DeploymentOperationProperties{
				TargetResource: &armresources.TargetResource{
					//nolint:lll
					ID: to.Ptr(
						"/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP/providers/Microsoft.Storage/storageAccounts/storage-account-name",
					),
					ResourceName: to.Ptr("storage-account-name"),
					ResourceType: to.Ptr("Microsoft.Storage/storageAccounts"),
				},
			},
		},
	},
}

func TestScopeGetResourceOperations(t *testing.T) {
	t.Run("SubscriptionScopeSuccess", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		deploymentService := mockazapi.NewDeploymentsServiceFromMockContext(mockContext)

		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet && strings.Contains(
				request.URL.Path,
				"/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Resources/deployments/DEPLOYMENT_NAME/operations",
			)
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			return mocks.CreateHttpResponseWithBody(
				request,
				http.StatusOK,
				armresources.DeploymentOperationsClientListAtScopeResponse{
					DeploymentOperationsListResult: deploymentOperationsListResult,
				},
			)
		})

		scope := newSubscriptionScope(deploymentService, "SUBSCRIPTION_ID", "eastus2")
		target := NewSubscriptionDeployment(
			scope,
			"DEPLOYMENT_NAME",
		)

		operations, err := target.Operations(*mockContext.Context)
		require.NoError(t, err)
		require.Len(t, operations, 1)
	})

	t.Run("ResourceGroupScopeSuccess", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		deploymentService := mockazapi.NewDeploymentsServiceFromMockContext(mockContext)

		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet && strings.Contains(
				request.URL.Path,
				"/subscriptions/SUBSCRIPTION_ID/resourcegroups/RESOURCE_GROUP/deployments/DEPLOYMENT_NAME/operations",
			)
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			return mocks.CreateHttpResponseWithBody(
				request,
				http.StatusOK,
				armresources.DeploymentOperationsClientListAtSubscriptionScopeResponse{
					DeploymentOperationsListResult: deploymentOperationsListResult,
				},
			)
		})

		scope := newResourceGroupScope(deploymentService, "SUBSCRIPTION_ID", "RESOURCE_GROUP")
		target := NewResourceGroupDeployment(
			scope,
			"DEPLOYMENT_NAME",
		)

		operations, err := target.Operations(*mockContext.Context)
		require.NoError(t, err)
		require.Len(t, operations, 1)
	})
}

var testArmParameters = azure.ArmParameters{
	"location": {
		Value: "West US",
	},
}

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
