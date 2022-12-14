// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package infra

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

type AzureResourceManager struct {
	azCli azcli.AzCli
}

type ResourceManager interface {
	GetDeploymentResourceOperations(
		ctx context.Context, scope Scope, queryStart *time.Time) ([]*armresources.DeploymentOperation, error)
	GetResourceTypeDisplayName(
		ctx context.Context,
		subscriptionId string,
		resourceId string,
		resourceType AzureResourceType,
	) (string, error)
	GetWebAppResourceTypeDisplayName(ctx context.Context, subscriptionId string, resourceId string) (string, error)
}

func NewAzureResourceManager(azCli azcli.AzCli) *AzureResourceManager {
	return &AzureResourceManager{
		azCli: azCli,
	}
}

func (rm *AzureResourceManager) GetDeploymentResourceOperations(
	ctx context.Context,
	scope Scope,
	queryStart *time.Time,
) ([]*armresources.DeploymentOperation, error) {
	// Gets all the scope level resource operations
	resourceOperations, err := scope.GetResourceOperations(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting subscription deployment: %w", err)
	}

	var resourceGroupName string
	resourceGroupScope, ok := scope.(*ResourceGroupScope)

	if ok {
		// If the scope is a resource group scope get the resource group directly
		resourceGroupName = resourceGroupScope.ResourceGroup()
	} else {
		// Otherwise find the resource group within the deployment operations
		for _, operation := range resourceOperations {
			if operation.Properties.TargetResource != nil &&
				*operation.Properties.TargetResource.ResourceType == string(AzureResourceTypeResourceGroup) {
				resourceGroupName = *operation.Properties.TargetResource.ResourceName
				break
			}
		}
	}

	if strings.TrimSpace(resourceGroupName) == "" {
		return resourceOperations, nil
	}

	// Find all resource group deployments within the subscription operations
	// Recursively append any resource group deployments that are found
	deploymentOperations := make(map[string]*armresources.DeploymentOperation)
	for _, operation := range resourceOperations {
		if operation.Properties.TargetResource != nil &&
			*operation.Properties.TargetResource.ResourceType == string(AzureResourceTypeDeployment) &&
			*operation.Properties.ProvisioningOperation == armresources.ProvisioningOperationCreate {
			err = rm.appendDeploymentResourcesRecursive(
				ctx,
				scope.SubscriptionId(),
				resourceGroupName,
				*operation.Properties.TargetResource.ResourceName,
				&deploymentOperations,
				queryStart,
			)
			if err != nil {
				return nil, fmt.Errorf("appending deployment resources: %w", err)
			}
		}
	}
	for _, op := range deploymentOperations {
		resourceOperations = append(resourceOperations, op)
	}
	return resourceOperations, nil
}

// GetResourceGroupsForDeployment returns the names of all the resource groups from a subscription level deployment.
func (rm *AzureResourceManager) GetResourceGroupsForDeployment(
	ctx context.Context,
	subscriptionId string,
	deploymentName string,
) ([]string, error) {
	deployment, err := rm.azCli.GetSubscriptionDeployment(ctx, subscriptionId, deploymentName)
	if err != nil {
		return nil, fmt.Errorf("fetching current deployment: %w", err)
	}

	// NOTE: it's possible for a deployment to list a resource group more than once. We're only interested in the
	// unique set.
	resourceGroups := map[string]struct{}{}

	for _, dependency := range deployment.Properties.Dependencies {
		for _, dependent := range dependency.DependsOn {
			if *dependent.ResourceType == string(AzureResourceTypeResourceGroup) {
				resourceGroups[*dependent.ResourceName] = struct{}{}
			}
		}
	}

	var keys []string

	for k := range resourceGroups {
		keys = append(keys, k)
	}

	return keys, nil
}

// GetResourceGroupsForEnvironment gets all resources groups for a given environment
func (rm *AzureResourceManager) GetResourceGroupsForEnvironment(
	ctx context.Context,
	env *environment.Environment,
) ([]azcli.AzCliResource, error) {
	res, err := rm.azCli.ListResourceGroup(ctx, env.GetSubscriptionId(), &azcli.ListResourceGroupOptions{
		TagFilter: &azcli.Filter{Key: "azd-env-name", Value: env.GetEnvName()},
	})

	if err != nil {
		return nil, err
	}

	if len(res) == 0 {
		return nil, azureutil.ResourceNotFound(
			fmt.Errorf("0 resource groups with azd-env-name with value: '%s'", env.GetEnvName()),
		)
	}

	return res, nil
}

// GetDefaultResourceGroups gets the default resource groups regardless of azd-env-name setting
// azd initially released with {envname}-rg for a default resource group name.  We now don't hardcode the default
// We search for them instead using the rg- prefix or -rg suffix
func (rm *AzureResourceManager) GetDefaultResourceGroups(
	ctx context.Context,
	env *environment.Environment,
) ([]azcli.AzCliResource, error) {
	allGroups, err := rm.azCli.ListResourceGroup(ctx, env.GetSubscriptionId(), nil)

	matchingGroups := []azcli.AzCliResource{}
	for _, group := range allGroups {
		if group.Name == fmt.Sprintf("rg-%[1]s", env.GetEnvName()) ||
			group.Name == fmt.Sprintf("%[1]s-rg", env.GetEnvName()) {
			matchingGroups = append(matchingGroups, group)
		}
	}

	if err != nil {
		return nil, err
	}

	if len(matchingGroups) == 0 {
		return nil, azureutil.ResourceNotFound(
			fmt.Errorf("0 resource groups with prefix or suffix with value: '%s'", env.GetEnvName()),
		)
	}

	return matchingGroups, nil
}

// FindResourceGroupForEnvironment will search for the resource group associated with an environment
// It will first try to find a resource group tagged with azd-env-name
// Then it will try to find a resource group that defaults to either {envname}-rg or rg-{envname}
// If it finds exactly one resource group, then it will use it
// If it finds more than one or zero resource groups, then it will prompt the user to update azure.yaml or
// AZURE_RESOURCE_GROUP
// with the resource group to use.
func (rm *AzureResourceManager) FindResourceGroupForEnvironment(
	ctx context.Context,
	env *environment.Environment,
) (string, error) {
	// Let's first try to find the resource group by environment name tag (azd-env-name)
	rgs, err := rm.GetResourceGroupsForEnvironment(ctx, env)
	var notFoundError *azureutil.ResourceNotFoundError
	if err != nil && !errors.As(err, &notFoundError) {
		return "", fmt.Errorf("getting resource group for environment: %s: %w", env.GetEnvName(), err)
	}

	if len(rgs) == 0 {
		// We didn't find any Resource Groups for the environment, now let's try to find Resource Groups with the
		// rg-{envname} prefix or {envname}-rg suffix
		rgs, err = rm.GetDefaultResourceGroups(ctx, env)
		if err != nil {
			return "", fmt.Errorf("getting default resource groups for environment: %s: %w", env.GetEnvName(), err)
		}
	}

	if len(rgs) == 1 && len(rgs[0].Name) > 0 {
		// We found one and only one RG, so we'll use it.
		return rgs[0].Name, nil
	}

	var msg string

	if len(rgs) > 1 {
		// We found more than one RG
		msg = "more than one possible resource group was found."
	} else {
		// We didn't find any RGs
		msg = "unable to find the environment resource group."
	}

	return "", fmt.Errorf(
		"%s please explicitly specify your resource group in azure.yaml or the AZURE_RESOURCE_GROUP environment variable",
		msg,
	)
}

func (rm *AzureResourceManager) GetResourceTypeDisplayName(
	ctx context.Context,
	subscriptionId string,
	resourceId string,
	resourceType AzureResourceType,
) (string, error) {
	if resourceType == AzureResourceTypeWebSite {
		// Web apps have different kinds of resources sharing the same resource type 'Microsoft.Web/sites', i.e. Function app
		// vs. App service It is extremely important that we display the right one, thus we resolve it by querying the
		// properties of the ARM resource.
		resourceTypeDisplayName, err := rm.GetWebAppResourceTypeDisplayName(ctx, subscriptionId, resourceId)

		if err != nil {
			return "", err
		} else {
			return resourceTypeDisplayName, nil
		}
	} else {
		resourceTypeDisplayName := GetResourceTypeDisplayName(resourceType)
		return resourceTypeDisplayName, nil
	}
}

func (rm *AzureResourceManager) GetWebAppResourceTypeDisplayName(
	ctx context.Context,
	subscriptionId string,
	resourceId string,
) (string, error) {
	resource, err := rm.azCli.GetResource(ctx, subscriptionId, resourceId)

	if err != nil {
		return "", fmt.Errorf("getting web app resource type display names: %w", err)
	}

	if strings.Contains(resource.Kind, "functionapp") {
		return "Function App", nil
	} else if strings.Contains(resource.Kind, "app") {
		return "App Service", nil
	} else {
		return "Web App", nil
	}
}

func (rm *AzureResourceManager) appendDeploymentResourcesRecursive(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	deploymentName string,
	resourceOperations *map[string]*armresources.DeploymentOperation,
	queryStart *time.Time,
) error {
	operations, err := rm.azCli.ListResourceGroupDeploymentOperations(
		ctx, subscriptionId, resourceGroupName, deploymentName)
	if err != nil {
		return fmt.Errorf("getting subscription deployment operations: %w", err)
	}

	for _, operation := range operations {
		if operation.Properties.TargetResource != nil &&
			operation.Properties.Timestamp != nil {
			_, alreadyAdded := (*resourceOperations)[*operation.OperationID]
			if *operation.Properties.TargetResource.ResourceType == string(AzureResourceTypeDeployment) {
				err := rm.appendDeploymentResourcesRecursive(
					ctx,
					subscriptionId,
					resourceGroupName,
					*operation.Properties.TargetResource.ResourceName,
					resourceOperations,
					queryStart,
				)
				if err != nil {
					return fmt.Errorf("appending deployment resources: %w", err)
				}
			} else if *operation.Properties.ProvisioningOperation == "Create" &&
				strings.TrimSpace(*operation.Properties.TargetResource.ResourceType) != "" &&
				!alreadyAdded &&
				operation.Properties.Timestamp.After(*queryStart) {
				(*resourceOperations)[*operation.OperationID] = operation
			}
		}
	}

	return nil
}
