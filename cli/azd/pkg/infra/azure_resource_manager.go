// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package infra

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/compare"
	"golang.org/x/exp/maps"
)

type AzureResourceManager struct {
	resourceService   *azapi.ResourceService
	deploymentService azapi.DeploymentService
}

type ResourceManager interface {
	GetDeploymentResourceOperations(
		ctx context.Context, deployment Deployment, queryStart *time.Time) ([]*armresources.DeploymentOperation, error)
	GetResourceTypeDisplayName(
		ctx context.Context,
		subscriptionId string,
		resourceId string,
		resourceType azapi.AzureResourceType,
	) (string, error)
	GetResourceGroupsForEnvironment(
		ctx context.Context,
		subscriptionId string,
		envName string,
	) ([]*azapi.Resource, error)
	FindResourceGroupForEnvironment(
		ctx context.Context,
		subscriptionId string,
		envName string,
	) (string, error)
}

func NewAzureResourceManager(
	resourceService *azapi.ResourceService,
	deploymentService azapi.DeploymentService,
) ResourceManager {
	return &AzureResourceManager{
		resourceService:   resourceService,
		deploymentService: deploymentService,
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
	rootDeploymentOperations, err := deployment.Operations(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting root deployment operations: %w", err)
	}

	operationMap := map[string]*armresources.DeploymentOperation{}
	if err := rm.appendDeploymentOperationsRecursive(ctx, queryStart, rootDeploymentOperations, operationMap); err != nil {
		return nil, err
	}

	return maps.Values(operationMap), nil
}

// GetResourceGroupsForEnvironment gets all resources groups for a given environment
func (rm *AzureResourceManager) GetResourceGroupsForEnvironment(
	ctx context.Context,
	subscriptionId string,
	envName string,
) ([]*azapi.Resource, error) {
	res, err := rm.resourceService.ListResourceGroup(ctx, subscriptionId, &azapi.ListResourceGroupOptions{
		TagFilter: &azapi.Filter{Key: azure.TagKeyAzdEnvName, Value: envName},
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
) ([]*azapi.Resource, error) {
	allGroups, err := rm.resourceService.ListResourceGroup(ctx, subscriptionId, nil)

	matchingGroups := []*azapi.Resource{}
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
	resourceType azapi.AzureResourceType,
) (string, error) {
	if resourceType == azapi.AzureResourceTypeWebSite {
		// Web apps have different kinds of resources sharing the same resource type 'Microsoft.Web/sites', i.e. Function app
		// vs. App service It is extremely important that we display the right one, thus we resolve it by querying the
		// properties of the ARM resource.
		resourceTypeDisplayName, err := rm.getWebAppResourceTypeDisplayName(ctx, subscriptionId, resourceId)

		if err != nil {
			return "", err
		} else {
			return resourceTypeDisplayName, nil
		}
	} else if resourceType == azapi.AzureResourceTypeCognitiveServiceAccount {
		resourceTypeDisplayName, err := rm.getCognitiveServiceResourceTypeDisplayName(ctx, subscriptionId, resourceId)

		if err != nil {
			return "", err
		} else {
			return resourceTypeDisplayName, nil
		}
	} else {
		resourceTypeDisplayName := azapi.GetResourceTypeDisplayName(resourceType)
		return resourceTypeDisplayName, nil
	}
}

// webAppApiVersion is the API Version we use when querying information about Web App resources
const webAppApiVersion = "2021-03-01"

func (rm *AzureResourceManager) getWebAppResourceTypeDisplayName(
	ctx context.Context,
	subscriptionId string,
	resourceId string,
) (string, error) {
	resource, err := rm.resourceService.GetResource(ctx, subscriptionId, resourceId, webAppApiVersion)

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
	resource, err := rm.resourceService.GetResource(ctx, subscriptionId, resourceId, cognitiveServiceApiVersion)

	if err != nil {
		return "", fmt.Errorf("getting cognitive service resource type display names: %w", err)
	}

	if strings.Contains(resource.Kind, "OpenAI") {
		return "Azure OpenAI", nil
	} else if strings.Contains(resource.Kind, "FormRecognizer") {
		return "Document Intelligence", nil
	} else {
		return "Azure AI Services", nil
	}
}

// appendDeploymentResourcesRecursive gets the leaf deployment operations and adds them to resourceOperations
// if they are not already in the list.
func (rm *AzureResourceManager) appendDeploymentOperationsRecursive(
	ctx context.Context,
	queryStart *time.Time,
	operations []*armresources.DeploymentOperation,
	operationMap map[string]*armresources.DeploymentOperation,
) error {
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

		// Process any nested deployments
		if *operation.Properties.TargetResource.ResourceType == string(azapi.AzureResourceTypeDeployment) &&
			*operation.Properties.ProvisioningOperation == armresources.ProvisioningOperationCreate {
			deploymentResourceId, err := arm.ParseResourceID(*operation.Properties.TargetResource.ID)
			if err != nil {
				return fmt.Errorf("parsing deployment resource ID: %w", err)
			}

			var nestedOperations []*armresources.DeploymentOperation
			var nestedError error

			if deploymentResourceId.ResourceGroupName == "" {
				nestedOperations, nestedError = rm.deploymentService.ListSubscriptionDeploymentOperations(
					ctx,
					deploymentResourceId.SubscriptionID,
					deploymentResourceId.Name)
			} else {
				nestedOperations, nestedError = rm.deploymentService.ListResourceGroupDeploymentOperations(
					ctx,
					deploymentResourceId.SubscriptionID,
					deploymentResourceId.ResourceGroupName,
					deploymentResourceId.Name,
				)
			}

			if nestedError != nil {
				return fmt.Errorf("getting deployment operations recursively: %w", nestedError)
			}

			if err = rm.appendDeploymentOperationsRecursive(ctx, queryStart, nestedOperations, operationMap); err != nil {
				return err
			}
		} else if *operation.Properties.ProvisioningOperation == armresources.ProvisioningOperationCreate &&
			// Only append CREATE operations that started after our queryStart time
			operation.Properties.Timestamp.After(*queryStart) {
			operationMap[*operation.ID] = operation
		}
	}

	return nil
}
