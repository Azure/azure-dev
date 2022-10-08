package azsdk

import (
	"context"
	"log"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

type MockDeploymentClient struct {
	onGetAtSubscriptionScope *armresources.DeploymentsClientGetAtSubscriptionScopeResponse
}

func (client *MockDeploymentClient) GetAtSubscriptionScope(ctx context.Context, deploymentName string) (armresources.DeploymentsClientGetAtSubscriptionScopeResponse, error) {
	if client.onGetAtSubscriptionScope == nil {
		log.Panic("missing mock configuration for test: onGetAtSubscriptionScope")
	}
	return *client.onGetAtSubscriptionScope, nil
}

func (client *MockDeploymentClient) OnGetAtSubscriptionScope(response *armresources.DeploymentsClientGetAtSubscriptionScopeResponse) {
	client.onGetAtSubscriptionScope = response
}
