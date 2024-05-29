package project

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

// ResourceManager provides a layer to query for Azure resource for azd project and services
// This would typically be used during deployment when azd need to deploy applications
// to the Azure resource hosting the application
type ResourceManager interface {
	GetResourceGroupName(
		ctx context.Context,
		subscriptionId string,
		resourceGroupTemplate osutil.ExpandableString,
	) (string, error)
	GetServiceResources(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		serviceConfig *ServiceConfig,
	) ([]azcli.AzCliResource, error)
	GetServiceResource(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		serviceConfig *ServiceConfig,
		rerunCommand string,
	) (azcli.AzCliResource, error)
	GetTargetResource(
		ctx context.Context,
		subscriptionId string,
		serviceConfig *ServiceConfig,
	) (*environment.TargetResource, error)
}

type resourceManager struct {
	env                  *environment.Environment
	azCli                azcli.AzCli
	deploymentOperations azapi.DeploymentOperations
}

// NewResourceManager creates a new instance of the project resource manager
func NewResourceManager(
	env *environment.Environment,
	azCli azcli.AzCli,
	deploymentOperations azapi.DeploymentOperations) ResourceManager {
	return &resourceManager{
		env:                  env,
		azCli:                azCli,
		deploymentOperations: deploymentOperations,
	}
}

// GetResourceGroupName gets the resource group name for the project.
//
// The resource group name is resolved in the following order:
//   - The user defined value in `azure.yaml`
//   - The user defined environment value `AZURE_RESOURCE_GROUP`
//
// - Resource group discovery by querying Azure Resources
// (see `resourceManager.FindResourceGroupForEnvironment` for more
// details)
func (rm *resourceManager) GetResourceGroupName(
	ctx context.Context,
	subscriptionId string,
	resourceGroupTemplate osutil.ExpandableString,
) (string, error) {
	name, err := resourceGroupTemplate.Envsubst(rm.env.Getenv)
	if err != nil {
		return "", err
	}

	if strings.TrimSpace(name) != "" {
		return name, nil
	}

	envResourceGroupName := rm.env.Getenv(environment.ResourceGroupEnvVarName)
	if envResourceGroupName != "" {
		return envResourceGroupName, nil
	}

	resourceManager := infra.NewAzureResourceManager(rm.azCli, rm.deploymentOperations)
	resourceGroupName, err := resourceManager.FindResourceGroupForEnvironment(ctx, subscriptionId, rm.env.Name())
	if err != nil {
		return "", err
	}

	return resourceGroupName, nil
}

// GetServiceResources finds azure service resources targeted by the service.
//
// If an explicit `ResourceName` is specified in `azure.yaml`, a resource with that name is searched for.
// Otherwise, searches for resources with a [azure.TagKeyAzdServiceName] tag set to the service key.
func (rm *resourceManager) GetServiceResources(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	serviceConfig *ServiceConfig,
) ([]azcli.AzCliResource, error) {
	filter := fmt.Sprintf("tagName eq '%s' and tagValue eq '%s'", azure.TagKeyAzdServiceName, serviceConfig.Name)

	subst, err := serviceConfig.ResourceName.Envsubst(rm.env.Getenv)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(subst) != "" {
		filter = fmt.Sprintf("name eq '%s'", subst)
	}

	return rm.azCli.ListResourceGroupResources(
		ctx,
		subscriptionId,
		resourceGroupName,
		&azcli.ListResourceGroupResourcesOptions{
			Filter: &filter,
		},
	)
}

// GetServiceResources gets the specific azure service resource targeted by the service.
//
// rerunCommand specifies the command that users should rerun in case of misconfiguration.
// This is included in the error message if applicable
func (rm *resourceManager) GetServiceResource(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	serviceConfig *ServiceConfig,
	rerunCommand string,
) (azcli.AzCliResource, error) {
	expandedResourceName, err := serviceConfig.ResourceName.Envsubst(rm.env.Getenv)
	if err != nil {
		return azcli.AzCliResource{}, fmt.Errorf("expanding name: %w", err)
	}

	resources, err := rm.GetServiceResources(ctx, subscriptionId, resourceGroupName, serviceConfig)
	if err != nil {
		return azcli.AzCliResource{}, fmt.Errorf("getting service resource: %w", err)
	}

	if expandedResourceName == "" { // A tag search was performed
		if len(resources) == 0 {
			err := fmt.Errorf(
				//nolint:lll
				"unable to find a resource tagged with '%s: %s'. Ensure the service resource is correctly tagged in your infrastructure configuration, and rerun %s",
				azure.TagKeyAzdServiceName,
				serviceConfig.Name,
				rerunCommand,
			)
			return azcli.AzCliResource{}, azureutil.ResourceNotFound(err)
		}

		if len(resources) != 1 {
			return azcli.AzCliResource{}, fmt.Errorf(
				//nolint:lll
				"expecting only '1' resource tagged with '%s: %s', but found '%d'. Ensure a unique service resource is correctly tagged in your infrastructure configuration, and rerun %s",
				azure.TagKeyAzdServiceName,
				serviceConfig.Name,
				len(resources),
				rerunCommand,
			)
		}
	} else { // Name based search
		if len(resources) == 0 {
			err := fmt.Errorf(
				"unable to find a resource with name '%s'. Ensure that resourceName in azure.yaml is valid, and rerun %s",
				expandedResourceName,
				rerunCommand)
			return azcli.AzCliResource{}, azureutil.ResourceNotFound(err)
		}

		// This can happen if multiple resources with different resource types are given the same name.
		if len(resources) != 1 {
			return azcli.AzCliResource{},
				fmt.Errorf(
					//nolint:lll
					"expecting only '1' resource named '%s', but found '%d'. Use a unique name for the service resource in the resource group '%s'",
					expandedResourceName,
					len(resources),
					resourceGroupName)
		}
	}

	return resources[0], nil
}

func (rm *resourceManager) GetTargetResource(
	ctx context.Context,
	subscriptionId string,
	serviceConfig *ServiceConfig,
) (*environment.TargetResource, error) {
	resourceGroupTemplate := serviceConfig.ResourceGroupName
	if resourceGroupTemplate.Empty() {
		resourceGroupTemplate = serviceConfig.Project.ResourceGroupName
	}

	resourceGroupName, err := rm.GetResourceGroupName(ctx, subscriptionId, resourceGroupTemplate)
	if err != nil {
		return nil, err
	}

	azureResource, err := rm.resolveServiceResource(ctx, subscriptionId, resourceGroupName, serviceConfig, "provision")
	if err != nil {
		return nil, err
	}

	return environment.NewTargetResource(
		subscriptionId,
		resourceGroupName,
		azureResource.Name,
		azureResource.Type,
	), nil
}

// resolveServiceResource resolves the service resource during service construction
func (rm *resourceManager) resolveServiceResource(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	serviceConfig *ServiceConfig,
	rerunCommand string,
) (azcli.AzCliResource, error) {
	azureResource, err := rm.GetServiceResource(ctx, subscriptionId, resourceGroupName, serviceConfig, rerunCommand)

	// If the service target supports delayed provisioning, the resource isn't expected to be found yet.
	// Return the empty resource
	var resourceNotFoundError *azureutil.ResourceNotFoundError
	if err != nil &&
		errors.As(err, &resourceNotFoundError) &&
		ServiceTargetKind(serviceConfig.Host).SupportsDelayedProvisioning() {
		return azureResource, nil
	}

	if err != nil {
		return azcli.AzCliResource{}, err
	}

	return azureResource, nil
}
