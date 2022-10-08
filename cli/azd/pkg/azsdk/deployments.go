// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azsdk

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/identity"
)

type DeploymentClientFactory func(subscriptionId string, credential azcore.TokenCredential) (DeploymentClient, error)

type contextKey string

const (
	deploymentClientFactoryContextKey contextKey = "deploymentClientFactoryContextKey"
)

func GetDeploymentClientFactory(ctx context.Context) DeploymentClientFactory {
	factory, ok := ctx.Value(deploymentClientFactoryContextKey).(DeploymentClientFactory)
	if ok {
		return factory
	}
	// return default factory
	return newDeploymentClient
}

func WithDeploymentFactory(ctx context.Context, factory DeploymentClientFactory) context.Context {
	return context.WithValue(ctx, deploymentClientFactoryContextKey, factory)
}

type DeploymentClient interface {
	GetAtSubscriptionScope(ctx context.Context, deploymentName string) (armresources.DeploymentsClientGetAtSubscriptionScopeResponse, error)
	GetResourceGroupDeployment(ctx context.Context, resourceGroupName string, deploymentName string) (armresources.DeploymentsClientGetResponse, error)
}

type deploymentClient struct {
	implClient *armresources.DeploymentsClient
}

func (client *deploymentClient) GetAtSubscriptionScope(ctx context.Context, deploymentName string) (armresources.DeploymentsClientGetAtSubscriptionScopeResponse, error) {
	return client.implClient.GetAtSubscriptionScope(ctx, deploymentName, nil)
}

func (client *deploymentClient) GetResourceGroupDeployment(ctx context.Context, resourceGroupName string, deploymentName string) (armresources.DeploymentsClientGetResponse, error) {
	return client.implClient.Get(ctx, resourceGroupName, deploymentName, nil)
}

func newDeploymentClient(subscriptionId string, credential azcore.TokenCredential) (DeploymentClient, error) {
	// Using default options for the client
	client, err := armresources.NewDeploymentsClient(subscriptionId, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("creating deployment client: %w", err)
	}

	return &deploymentClient{
		implClient: client,
	}, nil
}

func GetSubscriptionDeployment(
	ctx context.Context,
	subscriptionId string,
	deploymentName string) (result armresources.DeploymentExtended, err error) {
	credential, err := identity.GetCredentials(ctx)
	if err != nil {
		return result, fmt.Errorf("looking for credentials: %w", err)
	}

	clientFactory := GetDeploymentClientFactory(ctx)
	deploymentClient, err := clientFactory(subscriptionId, credential)
	if err != nil {
		return result, fmt.Errorf("creating deployments client: %w", err)
	}

	deployment, err := deploymentClient.GetAtSubscriptionScope(ctx, deploymentName)
	if err != nil {
		return result, fmt.Errorf("getting deployment from subscription: %w", err)
	}

	return deployment.DeploymentExtended, nil
}

func GetResourceGroupDeployment(
	ctx context.Context,
	subscriptionId,
	resourceGroupName,
	deploymentName string) (result armresources.DeploymentExtended, err error) {
	credential, err := identity.GetCredentials(ctx)
	if err != nil {
		return result, fmt.Errorf("looking for credentials: %w", err)
	}

	clientFactory := GetDeploymentClientFactory(ctx)
	deploymentClient, err := clientFactory(subscriptionId, credential)
	if err != nil {
		return result, fmt.Errorf("creating deployments client: %w", err)
	}

	deployment, err := deploymentClient.GetResourceGroupDeployment(ctx, resourceGroupName, deploymentName)
	if err != nil {
		return result, fmt.Errorf("getting deployment from resource group: %w", err)
	}

	return deployment.DeploymentExtended, nil
}
