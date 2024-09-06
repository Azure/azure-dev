package infra

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

var (
	ErrDeploymentsNotFound         = errors.New("no deployments found")
	ErrDeploymentResourcesNotFound = errors.New("no resources found for deployment")
)

type DeploymentManager struct {
	deploymentService azapi.DeploymentService
	resourceManager   ResourceManager
	console           input.Console
}

func NewDeploymentManager(
	deploymentService azapi.DeploymentService,
	resourceManager ResourceManager,
	console input.Console,
) *DeploymentManager {
	return &DeploymentManager{
		deploymentService: deploymentService,
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
	template azure.RawArmTemplate,
) (string, error) {
	return dm.deploymentService.CalculateTemplateHash(ctx, subscriptionId, template)
}

func (dm *DeploymentManager) ProgressDisplay(deployment Deployment) *ProvisioningProgressDisplay {
	return NewProvisioningProgressDisplay(dm.resourceManager, dm.console, deployment)
}

func (dm *DeploymentManager) SubscriptionScope(subscriptionId string, location string) *SubscriptionScope {
	return newSubscriptionScope(dm.deploymentService, subscriptionId, location)
}

func (dm *DeploymentManager) ResourceGroupScope(subscriptionId string, resourceGroupName string) *ResourceGroupScope {
	return newResourceGroupScope(dm.deploymentService, subscriptionId, resourceGroupName)
}

func (dm *DeploymentManager) SubscriptionDeployment(
	scope *SubscriptionScope,
	deploymentName string,
) *SubscriptionDeployment {
	return NewSubscriptionDeployment(scope, deploymentName)
}

func (dm *DeploymentManager) ResourceGroupDeployment(
	scope *ResourceGroupScope,
	deploymentName string,
) *ResourceGroupDeployment {
	return NewResourceGroupDeployment(scope, deploymentName)
}

func (dm *DeploymentManager) CompletedDeployments(
	ctx context.Context,
	scope Scope,
	envName string,
	hint string,
) ([]*azapi.ResourceDeployment, error) {
	deployments, err := scope.ListDeployments(ctx)
	if err != nil {
		return nil, err
	}

	slices.SortFunc(deployments, func(x, y *azapi.ResourceDeployment) int {
		return y.Timestamp.Compare(x.Timestamp)
	})

	// If hint is not provided, use the environment name as the hint
	if hint == "" {
		hint = envName
	}

	// Environment matching strategy
	// 1. Deployment with azd tagged env name
	// 2. Exact match on environment name to deployment name (old azd strategy)
	// 3. Multiple matching names based on specified hint (show user prompt)
	matchingDeployments := []*azapi.ResourceDeployment{}

	for _, deployment := range deployments {
		// We only want to consider deployments that are in a terminal state, not any which may be ongoing.
		if deployment.ProvisioningState != azapi.DeploymentProvisioningStateSucceeded &&
			deployment.ProvisioningState != azapi.DeploymentProvisioningStateFailed {
			continue
		}

		// Match on current azd strategy (tags) or old azd strategy (deployment name)
		if v, has := deployment.Tags[azure.TagKeyAzdEnvName]; has && *v == envName || deployment.Name == envName {
			return []*azapi.ResourceDeployment{deployment}, nil
		}

		// Fallback: Match on hint
		if hint != "" && strings.Contains(deployment.Name, hint) {
			matchingDeployments = append(matchingDeployments, deployment)
		}
	}

	if len(matchingDeployments) == 0 {
		return nil, fmt.Errorf("'%s': %w", envName, ErrDeploymentsNotFound)
	}

	return matchingDeployments, nil
}
