package bicep

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

type DeploymentTarget interface {
	// Deploy a given template with a set of parameters.
	Deploy(ctx context.Context, templatePath string, parametersPath string) error
	// GetDeployment fetches the result of the most recent deployment.
	GetDeployment(ctx context.Context) (azcli.AzCliDeployment, error)
}

// rgTarget is an implementation of `DeploymentTarget` for a resource group.
type rgTarget struct {
	// the CLI to use when deploying
	azCli azcli.AzCli
	// the subscription the resource group is located in.
	subscriptionId string
	// the resource group to deploy to.
	resourceGroupName string
	// the name of the deployment in the resource group.
	deploymentName string
}

// subTarget is an implementation of `DeploymentTarget` for a subscription.
type subTarget struct {
	// the CLI to use when deploying
	azCli azcli.AzCli
	// the subscription the resource group is located in.
	subscriptionId string
	// the name of the deployment in the resource group.
	deploymentName string
	// the location to store the deployment metadata in.
	location string
}

func NewResourceGroupDeploymentTarget(azCli azcli.AzCli, subscriptionId string, resourceGroupName string, deploymentName string) DeploymentTarget {
	return &rgTarget{azCli: azCli, deploymentName: deploymentName, subscriptionId: subscriptionId, resourceGroupName: resourceGroupName}
}

func NewSubscriptionDeploymentTarget(azCli azcli.AzCli, location string, subscriptionId string, deploymentName string) DeploymentTarget {
	return &subTarget{azCli: azCli, deploymentName: deploymentName, subscriptionId: subscriptionId, location: location}
}

func (target *rgTarget) Deploy(ctx context.Context, bicepPath string, parametersPath string) error {
	_, err := target.azCli.DeployToResourceGroup(ctx, target.subscriptionId, target.resourceGroupName, target.deploymentName, bicepPath, parametersPath)
	return err
}

func (target *rgTarget) GetDeployment(ctx context.Context) (azcli.AzCliDeployment, error) {
	return target.azCli.GetResourceGroupDeployment(ctx, target.subscriptionId, target.resourceGroupName, target.deploymentName)
}

func (target *subTarget) Deploy(ctx context.Context, bicepPath string, parametersPath string) error {
	_, err := target.azCli.DeployToSubscription(ctx, target.subscriptionId, target.deploymentName, bicepPath, parametersPath, target.location)
	return err
}

func (target *subTarget) GetDeployment(ctx context.Context) (azcli.AzCliDeployment, error) {
	return target.azCli.GetSubscriptionDeployment(ctx, target.subscriptionId, target.deploymentName)
}
