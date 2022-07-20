package provisioning

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type AzureDeploymentTarget interface {
	// Deploy a given template with a set of parameters.
	Deploy(ctx context.Context, templatePath string, parametersPath string) error
	// GetDeployment fetches the result of the most recent deployment.
	GetDeployment(ctx context.Context) (tools.AzCliDeployment, error)
}

// resourceGroupTarget is an implementation of `DeploymentTarget` for a resource group.
type resourceGroupTarget struct {
	// the CLI to use when deploying
	azCli tools.AzCli
	// the subscription the resource group is located in.
	subscriptionId string
	// the resource group to deploy to.
	resourceGroupName string
	// the name of the deployment in the resource group.
	deploymentName string
}

// subscriptionTarget is an implementation of `DeploymentTarget` for a subscription.
type subscriptionTarget struct {
	// the CLI to use when deploying
	azCli tools.AzCli
	// the subscription the resource group is located in.
	subscriptionId string
	// the name of the deployment in the resource group.
	deploymentName string
	// the location to store the deployment metadata in.
	location string
}

func NewResourceGroupDeploymentTarget(azCli tools.AzCli, subscriptionId string, resourceGroupName string, deploymentName string) AzureDeploymentTarget {
	return &resourceGroupTarget{azCli: azCli, deploymentName: deploymentName, subscriptionId: subscriptionId, resourceGroupName: resourceGroupName}
}

func NewSubscriptionDeploymentTarget(azCli tools.AzCli, location string, subscriptionId string, deploymentName string) AzureDeploymentTarget {
	return &subscriptionTarget{azCli: azCli, deploymentName: deploymentName, subscriptionId: subscriptionId, location: location}
}

func (target *resourceGroupTarget) Deploy(ctx context.Context, bicepPath string, parametersPath string) error {
	_, err := target.azCli.DeployToResourceGroup(ctx, target.subscriptionId, target.resourceGroupName, target.deploymentName, bicepPath, parametersPath)
	return err
}

func (target *resourceGroupTarget) GetDeployment(ctx context.Context) (tools.AzCliDeployment, error) {
	return target.azCli.GetResourceGroupDeployment(ctx, target.subscriptionId, target.resourceGroupName, target.deploymentName)
}

func (target *subscriptionTarget) Deploy(ctx context.Context, bicepPath string, parametersPath string) error {
	_, err := target.azCli.DeployToSubscription(ctx, target.subscriptionId, target.deploymentName, bicepPath, parametersPath, target.location)
	return err
}

func (target *subscriptionTarget) GetDeployment(ctx context.Context) (tools.AzCliDeployment, error) {
	return target.azCli.GetSubscriptionDeployment(ctx, target.subscriptionId, target.deploymentName)
}
