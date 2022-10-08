// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azsdk

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

type DeploymentClient interface {
	GetAtSubscriptionScope(ctx context.Context, deploymentName string) (armresources.DeploymentsClientGetAtSubscriptionScopeResponse, error)
}

type deploymentClient struct {
	implClient *armresources.DeploymentsClient
}

func (client deploymentClient) GetAtSubscriptionScope(ctx context.Context, deploymentName string) (armresources.DeploymentsClientGetAtSubscriptionScopeResponse, error) {
	return client.implClient.GetAtSubscriptionScope(ctx, deploymentName, nil)
}

func NewDeploymentClient(subscriptionId string, credential azcore.TokenCredential) (DeploymentClient, error) {
	// Using default options for the client
	client, err := armresources.NewDeploymentsClient(subscriptionId, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("creating deployment client: %w", err)
	}

	return deploymentClient{
		implClient: client,
	}, nil
}
