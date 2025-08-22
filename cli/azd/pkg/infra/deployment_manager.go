// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package infra

import (
	"context"
	"errors"
	"fmt"
	"log"
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
	layerName string,
	hint string,
) ([]*azapi.ResourceDeployment, error) {
	deployments, err := scope.ListDeployments(ctx)
	if err != nil {
		return nil, err
	}

	slices.SortFunc(deployments, func(x, y *azapi.ResourceDeployment) int {
		return y.Timestamp.Compare(x.Timestamp)
	})

	if hint == "" {
		// default hint for partial matches
		hint = envName

		if layerName != "" {
			hint = fmt.Sprintf("%s-%s", envName, layerName)
		}
	}

	// Environment matching strategy
	// 1. Deployment with azd tagged env name + layer name
	// 2. Exact match on environment name to deployment name (old azd strategy)
	// 3. Multiple matching names based specified hint (show user prompt)
	matchingDeployments := []*azapi.ResourceDeployment{}

	for _, deployment := range deployments {
		// We only want to consider deployments that are in a terminal state, not any which may be ongoing.
		if deployment.ProvisioningState != azapi.DeploymentProvisioningStateSucceeded &&
			deployment.ProvisioningState != azapi.DeploymentProvisioningStateFailed {
			continue
		}

		// Match on current azd strategy (tags)
		envTag, envTagHas := deployment.Tags[azure.TagKeyAzdEnvName]
		layerTag, layerTagHas := deployment.Tags[azure.TagKeyAzdLayerName]

		if envTagHas && *envTag == envName {
			if layerTagHas && *layerTag == layerName {
				log.Printf("completedDeployments: matched deployment '%s' using layerName: %s", deployment.Name, layerName)
				return []*azapi.ResourceDeployment{deployment}, nil
			}

			// If layerName is empty, we match on the envName alone
			if layerName == "" && !layerTagHas {
				log.Printf("completedDeployments: matched deployment '%s' using envName", deployment.Name)
				return []*azapi.ResourceDeployment{deployment}, nil
			}
		}

		// LEGACY: match on deployment name
		if deployment.Name == envName {
			return []*azapi.ResourceDeployment{deployment}, nil
		}

		// Fallback: Match on hint
		if strings.Contains(deployment.Name, hint) {
			matchingDeployments = append(matchingDeployments, deployment)
		}
	}

	if len(matchingDeployments) == 0 {
		return nil, fmt.Errorf("'%s': %w", hint, ErrDeploymentsNotFound)
	}

	return matchingDeployments, nil
}
