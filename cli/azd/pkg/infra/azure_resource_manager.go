// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package infra

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/compare"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

type AzureResourceManager struct {
	azCli                azcli.AzCli
	deploymentOperations azapi.DeploymentOperations
}

type ResourceManager interface {
	GetDeploymentResourceOperations(
		ctx context.Context, deployment Deployment, queryStart *time.Time) ([]*armresources.DeploymentOperation, error)
	GetResourceTypeDisplayName(
		ctx context.Context,
		subscriptionId string,
		resourceId string,
		resourceType AzureResourceType,
	) (string, error)
}

func NewAzureResourceManager(
	azCli azcli.AzCli, deploymentOperations azapi.DeploymentOperations) *AzureResourceManager {
	return &AzureResourceManager{
		azCli:                azCli,
		deploymentOperations: deploymentOperations,
	}
}

// GetDeploymentResourceOperations gets the list of all the resources created as part of the provided deployment.
// Each DeploymentOperation on the list holds a resource and the result of its deployment.
// One deployment operation can trigger new deployment operations, GetDeploymentResourceOperations traverses all
// operations recursively to find the leaf operations.
func (rm *AzureResourceManager) GetDeploymentResourceOperations(
	ctx context.Context,
	deployment Deployment,
	queryStart *time.Time,
) ([]*armresources.DeploymentOperation, error) {
	// Gets all the scope level resource operations
	topLevelDeploymentOperations, err := deployment.Operations(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting subscription deployment: %w", err)
	}

	subscriptionId := deployment.SubscriptionId()
	var resourceGroupName string
	resourceGroupDeployment, ok := deployment.(*ResourceGroupDeployment)

	if ok {
		// If the scope is a resource group scope get the resource group directly
		resourceGroupName = resourceGroupDeployment.ResourceGroupName()
	} else {
		// Otherwise find the resource group within the deployment operations
		for _, operation := range topLevelDeploymentOperations {
			if operation.Properties.TargetResource != nil &&
				*operation.Properties.TargetResource.ResourceType == string(AzureResourceTypeResourceGroup) {
				resourceGroupName = *operation.Properties.TargetResource.ResourceName
				break
			}
		}
	}

	if strings.TrimSpace(resourceGroupName) == "" {
		return topLevelDeploymentOperations, nil
	}

	allLevelsDeploymentOperations := []*armresources.DeploymentOperation{}
	resourceIdPrefix := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionId, resourceGroupName)

	// Find all resource group deployments within the subscription operations
	// Recursively append any resource group deployments that are found
	innerLevelDeploymentOperations := make(map[string]*armresources.DeploymentOperation)
	for _, operation := range topLevelDeploymentOperations {
		if operation.Properties.TargetResource == nil {
			// Operations w/o target data can't be resolved. Ignoring them
			continue
		}
		if !strings.HasPrefix(*operation.Properties.TargetResource.ID, resourceIdPrefix) {
			// topLevelDeploymentOperations might include deployments NOT within the resource group which we don't want to
			// resolve
			continue
		}
		if *operation.Properties.TargetResource.ResourceType != string(AzureResourceTypeDeployment) {
			// non-deploy ops can be part of the final result, we don't need to resolve inner level operations
			allLevelsDeploymentOperations = append(allLevelsDeploymentOperations, operation)
			continue
		}
		if *operation.Properties.TargetResource.ResourceType == string(AzureResourceTypeDeployment) &&
			*operation.Properties.ProvisioningOperation == armresources.ProvisioningOperationCreate {
			// for deploy/create ops, we want to traverse all inner operations to fetch resources.
			err = rm.appendDeploymentResourcesRecursive(
				ctx,
				subscriptionId,
				resourceGroupName,
				*operation.Properties.TargetResource.ResourceName,
				&innerLevelDeploymentOperations,
				queryStart,
			)
			if err != nil {
				return nil, fmt.Errorf("appending deployment resources: %w", err)
			}
		}
	}
	// Add the inner level dep-ops to the all level list to complete the result list
	for _, op := range innerLevelDeploymentOperations {
		allLevelsDeploymentOperations = append(allLevelsDeploymentOperations, op)
	}
	return allLevelsDeploymentOperations, nil
}

// GetResourceGroupsForEnvironment gets all resources groups for a given environment
func (rm *AzureResourceManager) GetResourceGroupsForEnvironment(
	ctx context.Context,
	subscriptionId string,
	envName string,
) ([]azcli.AzCliResource, error) {
	res, err := rm.azCli.ListResourceGroup(ctx, subscriptionId, &azcli.ListResourceGroupOptions{
		TagFilter: &azcli.Filter{Key: azure.TagKeyAzdEnvName, Value: envName},
	})

	if err != nil {
		return nil, err
	}

	if len(res) == 0 {
		return nil, azureutil.ResourceNotFound(
			fmt.Errorf("0 resource groups with tag '%s' with value: '%s'", azure.TagKeyAzdEnvName, envName),
		)
	}

	return res, nil
}

// GetDefaultResourceGroups gets the default resource groups regardless of azd-env-name setting
// azd initially released with {envname}-rg for a default resource group name.  We now don't hardcode the default
// We search for them instead using the rg- prefix or -rg suffix
func (rm *AzureResourceManager) GetDefaultResourceGroups(
	ctx context.Context,
	subscriptionId string,
	environmentName string,
) ([]azcli.AzCliResource, error) {
	allGroups, err := rm.azCli.ListResourceGroup(ctx, subscriptionId, nil)

	matchingGroups := []azcli.AzCliResource{}
	for _, group := range allGroups {
		if group.Name == fmt.Sprintf("rg-%[1]s", environmentName) ||
			group.Name == fmt.Sprintf("%[1]s-rg", environmentName) {
			matchingGroups = append(matchingGroups, group)
		}
	}

	if err != nil {
		return nil, err
	}

	if len(matchingGroups) == 0 {
		return nil, azureutil.ResourceNotFound(
			fmt.Errorf("0 resource groups with prefix or suffix with value: '%s'", environmentName),
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
	subscriptionId string,
	envName string,
) (string, error) {
	// Let's first try to find the resource group by environment name tag (azd-env-name)
	rgs, err := rm.GetResourceGroupsForEnvironment(ctx, subscriptionId, envName)
	var notFoundError *azureutil.ResourceNotFoundError
	if err != nil && !errors.As(err, &notFoundError) {
		return "", fmt.Errorf("getting resource group for environment: %s: %w", envName, err)
	}

	if len(rgs) == 0 {
		// We didn't find any Resource Groups for the environment, now let's try to find Resource Groups with the
		// rg-{envname} prefix or {envname}-rg suffix
		rgs, err = rm.GetDefaultResourceGroups(ctx, subscriptionId, envName)
		if err != nil {
			return "", fmt.Errorf("getting default resource groups for environment: %s: %w", envName, err)
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
		"%s explicitly specify your resource group in azure.yaml or the AZURE_RESOURCE_GROUP environment variable",
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
		resourceTypeDisplayName, err := rm.getWebAppResourceTypeDisplayName(ctx, subscriptionId, resourceId)

		if err != nil {
			return "", err
		} else {
			return resourceTypeDisplayName, nil
		}
	} else if resourceType == AzureResourceTypeCognitiveServiceAccount {
		resourceTypeDisplayName, err := rm.getCognitiveServiceResourceTypeDisplayName(ctx, subscriptionId, resourceId)

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

// cWebAppApiVersion is the API Version we use when querying information about Web App resources
const cWebAppApiVersion = "2021-03-01"

func (rm *AzureResourceManager) getWebAppResourceTypeDisplayName(
	ctx context.Context,
	subscriptionId string,
	resourceId string,
) (string, error) {
	resource, err := rm.azCli.GetResource(ctx, subscriptionId, resourceId, cWebAppApiVersion)

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

// cognitiveServiceApiVersion is the API Version we use when querying information about Cognitive Service resources
const cognitiveServiceApiVersion = "2021-04-30"

func (rm *AzureResourceManager) getCognitiveServiceResourceTypeDisplayName(
	ctx context.Context,
	subscriptionId string,
	resourceId string,
) (string, error) {
	resource, err := rm.azCli.GetResource(ctx, subscriptionId, resourceId, cognitiveServiceApiVersion)

	if err != nil {
		return "", fmt.Errorf("getting cognitive service resource type display names: %w", err)
	}

	if strings.Contains(resource.Kind, "OpenAI") {
		return "Azure OpenAI", nil
	} else if strings.Contains(resource.Kind, "FormRecognizer") {
		return "Document Intelligence", nil
	} else {
		return "Cognitive Service", nil
	}
}

// appendDeploymentResourcesRecursive gets the leaf deployment operations and adds them to resourceOperations
// if they are not already in the list.
func (rm *AzureResourceManager) appendDeploymentResourcesRecursive(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	deploymentName string,
	resourceOperations *map[string]*armresources.DeploymentOperation,
	queryStart *time.Time,
) error {
	operations, err := rm.deploymentOperations.ListResourceGroupDeploymentOperations(
		ctx, subscriptionId, resourceGroupName, deploymentName)
	if err != nil {
		// Don't return an error upon getting the deployment operations from deploymentName.
		// That's because returning an error would stop traversing the entire deployments graph and prevent
		// the caller function from getting any deployment operation at all.
		// So, instead of returning error, ignore the problematic deployment node and log an error about it.
		// This will allow the caller to get as much information about the deployments operations as possible.
		log.Printf("getting subscription deployment operations: %s", err.Error())
		return nil
	}

	for _, operation := range operations {
		// Operations w/o target data can't be resolved. Ignoring them
		if operation.Properties.TargetResource == nil ||
			// The time stamp is used to filter only records after the queryStart.
			// We ignore the resource if we can't know when it was created
			operation.Properties.Timestamp == nil ||
			// The resource type is required to resolve the name of the resource.
			// If the dep-op is missing this, we can't resolve it.
			compare.IsStringNilOrEmpty(operation.Properties.TargetResource.ResourceType) {
			continue
		}

		if compare.PtrValueEquals(operation.Properties.TargetResource.ResourceType, string(AzureResourceTypeDeployment)) {
			// go to inner levels to resolve resources
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
			continue
		}

		_, alreadyAdded := (*resourceOperations)[*operation.OperationID]
		if !alreadyAdded &&
			*operation.Properties.ProvisioningOperation == armresources.ProvisioningOperationCreate &&
			operation.Properties.Timestamp.After(*queryStart) {
			(*resourceOperations)[*operation.OperationID] = operation
		}
	}

	return nil
}
