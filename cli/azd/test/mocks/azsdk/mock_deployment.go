package azsdk

import (
	"context"
	"log"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

type MockDeploymentClient struct {
	onGetAtSubscriptionScope     *armresources.DeploymentsClientGetAtSubscriptionScopeResponse
	onGetResourceGroupDeployment *armresources.DeploymentsClientGetResponse
}

func (client *MockDeploymentClient) GetResourceGroupDeployment(ctx context.Context, resourceGroupName string, deploymentName string) (armresources.DeploymentsClientGetResponse, error) {
	if client.onGetResourceGroupDeployment == nil {
		log.Panic("missing mock configuration for test: onGetResourceGroupDeployment")
	}
	return *client.onGetResourceGroupDeployment, nil
}

func (client *MockDeploymentClient) WhenGetResourceGroupDeployment(response *armresources.DeploymentsClientGetResponse) {
	client.onGetResourceGroupDeployment = response
}

func (client *MockDeploymentClient) GetAtSubscriptionScope(ctx context.Context, deploymentName string) (armresources.DeploymentsClientGetAtSubscriptionScopeResponse, error) {
	if client.onGetAtSubscriptionScope == nil {
		log.Panic("missing mock configuration for test: onGetAtSubscriptionScope")
	}
	return *client.onGetAtSubscriptionScope, nil
}

func (client *MockDeploymentClient) WhenGetAtSubscriptionScope(response *armresources.DeploymentsClientGetAtSubscriptionScopeResponse) {
	client.onGetAtSubscriptionScope = response
}
