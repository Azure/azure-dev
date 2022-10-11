// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azcli

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/identity"
)

func (cli *azCli) GetSubscriptionDeployment(ctx context.Context, subscriptionId string, deploymentName string) (result armresources.DeploymentExtended, err error) {
	credential, err := identity.GetCredentials(ctx)
	if err != nil {
		return result, fmt.Errorf("looking for credentials: %w", err)
	}

	deploymentClient, err := armresources.NewDeploymentsClient(
		subscriptionId, credential, cli.createArmClientOptions(ctx))
	if err != nil {
		return result, fmt.Errorf("creating deployments client: %w", err)
	}

	deployment, err := deploymentClient.GetAtSubscriptionScope(ctx, deploymentName, nil)
	if err != nil {
		return result, fmt.Errorf("getting deployment from subscription: %w", err)
	}

	return deployment.DeploymentExtended, nil
}

func (cli *azCli) GetResourceGroupDeployment(ctx context.Context, subscriptionId string, resourceGroupName string, deploymentName string) (result armresources.DeploymentExtended, err error) {
	credential, err := identity.GetCredentials(ctx)
	if err != nil {
		return result, fmt.Errorf("looking for credentials: %w", err)
	}

	deploymentClient, err := armresources.NewDeploymentsClient(
		subscriptionId, credential, cli.createArmClientOptions(ctx))
	if err != nil {
		return result, fmt.Errorf("creating deployments client: %w", err)
	}

	deployment, err := deploymentClient.Get(ctx, resourceGroupName, deploymentName, nil)
	if err != nil {
		var errDetails *azcore.ResponseError
		errors.As(err, &errDetails)
		if errDetails.StatusCode == 404 {
			return result, ErrDeploymentNotFound
		}
		return result, fmt.Errorf("getting deployment from resource group: %w", err)
	}

	return deployment.DeploymentExtended, nil
}
