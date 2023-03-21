// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azcli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/internal"
)

func (cli *azCli) GetSubscriptionDeployment(
	ctx context.Context,
	subscriptionId string,
	deploymentName string,
) (*armresources.DeploymentExtended, error) {
	deploymentClient, err := cli.createDeploymentsClient(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("creating deployments client: %w", err)
	}

	deployment, err := deploymentClient.GetAtSubscriptionScope(ctx, deploymentName, nil)
	if err != nil {
		var errDetails *azcore.ResponseError
		if errors.As(err, &errDetails) && errDetails.StatusCode == 404 {
			return nil, ErrDeploymentNotFound
		}
		return nil, fmt.Errorf("getting deployment from subscription: %w", err)
	}

	return &deployment.DeploymentExtended, nil
}

func (cli *azCli) GetResourceGroupDeployment(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	deploymentName string,
) (*armresources.DeploymentExtended, error) {
	deploymentClient, err := cli.createDeploymentsClient(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("creating deployments client: %w", err)
	}

	deployment, err := deploymentClient.Get(ctx, resourceGroupName, deploymentName, nil)
	if err != nil {
		var errDetails *azcore.ResponseError
		if errors.As(err, &errDetails) && errDetails.StatusCode == 404 {
			return nil, ErrDeploymentNotFound
		}
		return nil, fmt.Errorf("getting deployment from resource group: %w", err)
	}

	return &deployment.DeploymentExtended, nil
}

func (cli *azCli) createDeploymentsClient(
	ctx context.Context,
	subscriptionId string,
) (*armresources.DeploymentsClient, error) {
	options := cli.createDefaultClientOptionsBuilder(ctx).BuildArmClientOptions()
	client, err := armresources.NewDeploymentsClient(subscriptionId, cli.credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating deployments client: %w", err)
	}

	return client, nil
}

func (cli *azCli) DeployToSubscription(
	ctx context.Context,
	subscriptionId, deploymentName string,
	armTemplate azure.RawArmTemplate,
	parameters azure.ArmParameters,
	location string,
) (AzCliDeploymentResult, error) {
	deploymentClient, err := cli.createDeploymentsClient(ctx, subscriptionId)
	if err != nil {
		return AzCliDeploymentResult{}, fmt.Errorf("creating deployments client: %w", err)
	}

	createFromTemplateOperation, err := deploymentClient.BeginCreateOrUpdateAtSubscriptionScope(
		ctx, deploymentName,
		armresources.Deployment{
			Properties: &armresources.DeploymentProperties{
				Template:   armTemplate,
				Parameters: parameters,
				Mode:       to.Ptr(armresources.DeploymentModeIncremental),
			},
			Location: to.Ptr(location),
		}, nil)
	if err != nil {
		return AzCliDeploymentResult{}, fmt.Errorf("starting deployment to subscription: %w", err)
	}

	// wait for deployment creation
	deployResult, err := createFromTemplateOperation.PollUntilDone(ctx, nil)
	if err != nil {
		deploymentError := createDeploymentError(err)
		return AzCliDeploymentResult{}, fmt.Errorf(
			"deploying to subscription:\n\nDeployment Error Details:\n%w",
			deploymentError,
		)
	}

	return AzCliDeploymentResult{
		Properties: AzCliDeploymentResultProperties{
			Outputs: CreateDeploymentOutput(deployResult.Properties.Outputs),
		},
	}, nil
}

func (cli *azCli) DeployToResourceGroup(
	ctx context.Context,
	subscriptionId, resourceGroup, deploymentName string,
	armTemplate azure.RawArmTemplate,
	parameters azure.ArmParameters,
) (AzCliDeploymentResult, error) {
	deploymentClient, err := cli.createDeploymentsClient(ctx, subscriptionId)
	if err != nil {
		return AzCliDeploymentResult{}, fmt.Errorf("creating deployments client: %w", err)
	}

	createFromTemplateOperation, err := deploymentClient.BeginCreateOrUpdate(
		ctx, resourceGroup, deploymentName,
		armresources.Deployment{
			Properties: &armresources.DeploymentProperties{
				Template:   armTemplate,
				Parameters: parameters,
				Mode:       to.Ptr(armresources.DeploymentModeIncremental),
			},
		}, nil)
	if err != nil {
		return AzCliDeploymentResult{}, fmt.Errorf("starting deployment to resource group: %w", err)
	}

	// wait for deployment creation
	deployResult, err := createFromTemplateOperation.PollUntilDone(ctx, nil)
	if err != nil {
		deploymentError := createDeploymentError(err)
		return AzCliDeploymentResult{}, fmt.Errorf(
			"deploying to resource group:\n\nDeployment Error Details:\n%w",
			deploymentError,
		)
	}

	return AzCliDeploymentResult{
		Properties: AzCliDeploymentResultProperties{
			Outputs: CreateDeploymentOutput(deployResult.Properties.Outputs),
		},
	}, nil
}

func (cli *azCli) DeleteSubscriptionDeployment(ctx context.Context, subscriptionId string, deploymentName string) error {
	deploymentClient, err := cli.createDeploymentsClient(ctx, subscriptionId)
	if err != nil {
		return fmt.Errorf("deleting deployment: %w", err)
	}

	deleteDeploymentOperation, err := deploymentClient.BeginDeleteAtSubscriptionScope(ctx, deploymentName, nil)
	if err != nil {
		return fmt.Errorf("starting to delete deployment: %w", err)
	}

	// wait for the operation to complete
	_, err = deleteDeploymentOperation.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("deleting deployment operation: %w", err)
	}

	return nil
}

// convert from: sdk client outputs: interface{} to map[string]azcli.AzCliDeploymentOutput
// sdk client parses http response from network as an interface{}
// this function keeps the compatibility with the previous AzCliDeploymentOutput model
func CreateDeploymentOutput(rawOutputs interface{}) (result map[string]AzCliDeploymentOutput) {
	if rawOutputs == nil {
		return make(map[string]AzCliDeploymentOutput, 0)
	}

	castInput := rawOutputs.(map[string]interface{})
	result = make(map[string]AzCliDeploymentOutput, len(castInput))
	for key, output := range castInput {
		innerValue := output.(map[string]interface{})
		result[key] = AzCliDeploymentOutput{
			Type:  innerValue["type"].(string),
			Value: innerValue["value"],
		}
	}
	return result
}

// Attempts to create an Azure Deployment error from the HTTP response error
func createDeploymentError(err error) error {
	var responseErr *azcore.ResponseError
	if errors.As(err, &responseErr) {
		var errorText string
		rawBody, err := io.ReadAll(responseErr.RawResponse.Body)
		if err != nil {
			errorText = responseErr.Error()
		} else {
			errorText = string(rawBody)
		}
		return internal.NewAzureDeploymentError(errorText)
	}

	return err
}
