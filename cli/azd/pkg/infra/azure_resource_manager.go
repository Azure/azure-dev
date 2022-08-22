package infra

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

type AzureResourceManager struct {
	azCli azcli.AzCli
}

func NewAzureResourceManager(azCli azcli.AzCli) *AzureResourceManager {
	return &AzureResourceManager{
		azCli: azCli,
	}
}

func (rm *AzureResourceManager) GetDeploymentResourceOperations(ctx context.Context, subscriptionId string, deploymentName string) ([]azcli.AzCliResourceOperation, error) {
	// Gets all the subscription level deployments
	subOperations, err := rm.azCli.ListSubscriptionDeploymentOperations(ctx, subscriptionId, deploymentName)
	if err != nil {
		return nil, fmt.Errorf("getting subscription deployment: %w", err)
	}

	var resourceGroupName string

	// Find the resource group
	for _, operation := range subOperations {
		if operation.Properties.TargetResource.ResourceType == string(AzureResourceTypeResourceGroup) {
			resourceGroupName = operation.Properties.TargetResource.ResourceName
			break
		}
	}

	resourceOperations := []azcli.AzCliResourceOperation{}

	if strings.TrimSpace(resourceGroupName) == "" {
		return resourceOperations, nil
	}

	// Find all resource group deployments within the subscription operations
	// Recursively append any resource group deployments that are found
	for _, operation := range subOperations {
		if operation.Properties.TargetResource.ResourceType == string(AzureResourceTypeDeployment) {
			err = rm.appendDeploymentResourcesRecursive(ctx, subscriptionId, resourceGroupName, operation.Properties.TargetResource.ResourceName, &resourceOperations)
			if err != nil {
				return nil, fmt.Errorf("appending deployment resources: %w", err)
			}
		}
	}

	return resourceOperations, nil
}

// GetResourceGroupsForDeployment returns the names of all the resource groups from a subscription level deployment.
func (rm *AzureResourceManager) GetResourceGroupsForDeployment(ctx context.Context, subscriptionId string, deploymentName string) ([]string, error) {
	deployment, err := rm.azCli.GetSubscriptionDeployment(ctx, subscriptionId, deploymentName)
	if err != nil {
		return nil, fmt.Errorf("fetching current deployment: %w", err)
	}

	// NOTE: it's possible for a deployment to list a resource group more than once. We're only interested in the
	// unique set.
	resourceGroups := map[string]struct{}{}

	for _, dependency := range deployment.Properties.Dependencies {
		for _, dependent := range dependency.DependsOn {
			if dependent.ResourceType == string(AzureResourceTypeResourceGroup) {
				resourceGroups[dependent.ResourceName] = struct{}{}
			}
		}
	}

	var keys []string

	for k := range resourceGroups {
		keys = append(keys, k)
	}

	return keys, nil
}

func (rm *AzureResourceManager) appendDeploymentResourcesRecursive(ctx context.Context, subscriptionId string, resourceGroupName string, deploymentName string, resourceOperations *[]azcli.AzCliResourceOperation) error {
	operations, err := rm.azCli.ListResourceGroupDeploymentOperations(ctx, subscriptionId, resourceGroupName, deploymentName)
	if err != nil {
		return fmt.Errorf("getting subscription deployment operations: %w", err)
	}

	for _, operation := range operations {
		if operation.Properties.TargetResource.ResourceType == string(AzureResourceTypeDeployment) {
			err := rm.appendDeploymentResourcesRecursive(ctx, subscriptionId, resourceGroupName, operation.Properties.TargetResource.ResourceName, resourceOperations)
			if err != nil {
				return fmt.Errorf("appending deployment resources: %w", err)
			}
		} else if operation.Properties.ProvisioningOperation == "Create" && strings.TrimSpace(operation.Properties.TargetResource.ResourceType) != "" {
			*resourceOperations = append(*resourceOperations, operation)
		}
	}

	return nil
}

func (rm *AzureResourceManager) GetResourceTypeDisplayName(ctx context.Context, subscriptionId string, resourceId string, resourceType AzureResourceType) (string, error) {
	if resourceType == AzureResourceTypeWebSite {
		// Web apps have different kinds of resources sharing the same resource type 'Microsoft.Web/sites', i.e. Function app vs. App service
		// It is extremely important that we display the right one, thus we resolve it by querying the properties of the ARM resource.
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

func (rm *AzureResourceManager) GetWebAppResourceTypeDisplayName(ctx context.Context, subscriptionId string, resourceId string) (string, error) {
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
