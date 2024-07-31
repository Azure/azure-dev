package infra

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

type DeploymentManager struct {
	deploymentService azapi.Deployments
	operationService  azapi.DeploymentOperations
	resourceManager   ResourceManager
	console           input.Console
}

func NewDeploymentManager(
	deploymentService azapi.Deployments,
	operationService azapi.DeploymentOperations,
	resourceManager ResourceManager,
	console input.Console,
) *DeploymentManager {
	return &DeploymentManager{
		deploymentService: deploymentService,
		operationService:  operationService,
		resourceManager:   resourceManager,
		console:           console,
	}
}

func (dm *DeploymentManager) GenerateDeploymentName(baseName string) string {
	return dm.deploymentService.GenerateDeploymentName(baseName)
}

func (dm *DeploymentManager) CalculateTemplateHash(
	ctx context.Context,
	subscriptionId string,
	template azure.RawArmTemplate) (armresources.DeploymentsClientCalculateTemplateHashResponse, error) {
	return dm.deploymentService.CalculateTemplateHash(ctx, subscriptionId, template)
}

func (dm *DeploymentManager) ProgressDisplay(deployment Deployment) *ProvisioningProgressDisplay {
	return NewProvisioningProgressDisplay(dm.resourceManager, dm.console, deployment)
}

func (dm *DeploymentManager) SubscriptionScope(subscriptionId string) *SubscriptionScope {
	return newSubscriptionScope(dm.deploymentService, dm.operationService, subscriptionId)
}

func (dm *DeploymentManager) ResourceGroupScope(subscriptionId string, resourceGroupName string) *ResourceGroupScope {
	return newResourceGroupScope(dm.deploymentService, dm.operationService, subscriptionId, resourceGroupName)
}

func (dm *DeploymentManager) SubscriptionDeployment(
	scope *SubscriptionScope,
	location string,
	deploymentName string,
) *SubscriptionDeployment {
	return NewSubscriptionDeployment(scope, location, deploymentName)
}

func (dm *DeploymentManager) ResourceGroupDeployment(
	scope *ResourceGroupScope,
	deploymentName string,
) *ResourceGroupDeployment {
	return NewResourceGroupDeployment(scope, deploymentName)
}
