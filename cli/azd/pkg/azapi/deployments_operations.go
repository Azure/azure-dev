// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

type DeploymentOperations interface {
	ListSubscriptionDeploymentOperations(
		ctx context.Context,
		deploymentName string,
	) ([]*armresources.DeploymentOperation, error)
	ListResourceGroupDeploymentOperations(
		ctx context.Context,
		resourceGroupName string,
		deploymentName string,
	) ([]*armresources.DeploymentOperation, error)
}

func NewDeploymentOperations(
	deploymentOperationsClient *armresources.DeploymentOperationsClient,
) DeploymentOperations {
	return &deploymentOperations{
		deploymentOperationsClient: deploymentOperationsClient,
	}
}

type deploymentOperations struct {
	deploymentOperationsClient *armresources.DeploymentOperationsClient
}

func (dp *deploymentOperations) ListSubscriptionDeploymentOperations(
	ctx context.Context,
	deploymentName string,
) ([]*armresources.DeploymentOperation, error) {
	result := []*armresources.DeploymentOperation{}

	// Get all without any filter
	getDeploymentsPager := dp.deploymentOperationsClient.NewListAtSubscriptionScopePager(deploymentName, nil)

	for getDeploymentsPager.More() {
		page, err := getDeploymentsPager.NextPage(ctx)
		var errDetails *azcore.ResponseError
		if errors.As(err, &errDetails) && errDetails.StatusCode == 404 {
			return nil, ErrDeploymentNotFound
		}
		if err != nil {
			return nil, fmt.Errorf("failed getting list of deployment operations: %w", err)
		}
		result = append(result, page.Value...)
	}

	return result, nil
}

func (dp *deploymentOperations) ListResourceGroupDeploymentOperations(
	ctx context.Context,
	resourceGroupName string,
	deploymentName string,
) ([]*armresources.DeploymentOperation, error) {
	result := []*armresources.DeploymentOperation{}

	// Get all without any filter
	getDeploymentsPager := dp.deploymentOperationsClient.NewListPager(resourceGroupName, deploymentName, nil)

	for getDeploymentsPager.More() {
		page, err := getDeploymentsPager.NextPage(ctx)
		var errDetails *azcore.ResponseError
		if errors.As(err, &errDetails) && errDetails.StatusCode == 404 {
			return nil, ErrDeploymentNotFound
		}
		if err != nil {
			return nil, fmt.Errorf("failed getting list of deployment operations from resource group: %w", err)
		}
		result = append(result, page.Value...)
	}

	return result, nil
}
