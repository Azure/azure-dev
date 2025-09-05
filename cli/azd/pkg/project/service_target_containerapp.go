// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"log"
	"strconv"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/containerapps"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type containerAppTarget struct {
	env                 *environment.Environment
	envManager          environment.Manager
	containerHelper     *ContainerHelper
	containerAppService containerapps.ContainerAppService
	resourceManager     ResourceManager
}

// NewContainerAppTarget creates the container app service target.
//
// The target resource can be partially filled with only ResourceGroupName, since container apps
// can be provisioned during deployment.
func NewContainerAppTarget(
	env *environment.Environment,
	envManager environment.Manager,
	containerHelper *ContainerHelper,
	containerAppService containerapps.ContainerAppService,
	resourceManager ResourceManager,
) ServiceTarget {
	return &containerAppTarget{
		env:                 env,
		envManager:          envManager,
		containerHelper:     containerHelper,
		containerAppService: containerAppService,
		resourceManager:     resourceManager,
	}
}

// Gets the required external tools
func (at *containerAppTarget) RequiredExternalTools(ctx context.Context, serviceConfig *ServiceConfig) []tools.ExternalTool {
	return at.containerHelper.RequiredExternalTools(ctx, serviceConfig)
}

// Initializes the Container App target
func (at *containerAppTarget) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	if err := at.addPreProvisionChecks(ctx, serviceConfig); err != nil {
		return fmt.Errorf("initializing container app target: %w", err)
	}

	return nil
}

// Prepares and tags the container image from the build output based on the specified service configuration
func (at *containerAppTarget) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	return packageOutput, nil
}

// Publish pushes the container image to ACR
func (at *containerAppTarget) Publish(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
	publishOptions *PublishOptions,
) (*ServicePublishResult, error) {
	if err := at.validateTargetResource(targetResource); err != nil {
		return nil, fmt.Errorf("validating target resource: %w", err)
	}

	// Login, tag & push container image to ACR
	publishResult, err := at.containerHelper.Publish(
		ctx, serviceConfig, packageOutput, targetResource, progress, publishOptions)
	if err != nil {
		return nil, err
	}

	// Save the name of the image we pushed into the environment with a well known key.
	log.Printf("writing image name to environment")

	containerDetails, ok := publishResult.Details.(*ContainerPublishDetails)
	if !ok {
		return nil, fmt.Errorf("expected ContainerPublishDetails but got %T", publishResult.Details)
	}
	at.env.SetServiceProperty(serviceConfig.Name, "IMAGE_NAME", containerDetails.RemoteImage)

	if err := at.envManager.Save(ctx, at.env); err != nil {
		return nil, fmt.Errorf("saving image name to environment: %w", err)
	}

	return publishResult, nil
}

// Deploys the container app service using the published image.
func (at *containerAppTarget) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
	servicePublishResult *ServicePublishResult,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	if err := at.validateTargetResource(targetResource); err != nil {
		return nil, fmt.Errorf("validating target resource: %w", err)
	}

	// Get the image name from the publish result
	if servicePublishResult == nil {
		return nil, fmt.Errorf("unexpected publish result for service: %s", serviceConfig.Name)
	}

	containerDetails, ok := servicePublishResult.Details.(*ContainerPublishDetails)
	if !ok {
		return nil, fmt.Errorf("expected ContainerPublishDetails but got %T", servicePublishResult.Details)
	}
	imageName := containerDetails.RemoteImage

	containerAppOptions := containerapps.ContainerAppOptions{
		ApiVersion: serviceConfig.ApiVersion,
	}

	progress.SetProgress(NewServiceProgress("Updating container app revision"))
	err := at.containerAppService.AddRevision(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
		imageName,
		&containerAppOptions,
	)
	if err != nil {
		return nil, fmt.Errorf("updating container app service: %w", err)
	}

	progress.SetProgress(NewServiceProgress("Fetching endpoints for container app service"))
	endpoints, err := at.Endpoints(ctx, serviceConfig, targetResource)
	if err != nil {
		return nil, err
	}

	return &ServiceDeployResult{
		Package: packageOutput,
		Publish: servicePublishResult,
		TargetResourceId: azure.ContainerAppRID(
			targetResource.SubscriptionId(),
			targetResource.ResourceGroupName(),
			targetResource.ResourceName(),
		),
		Kind:      ContainerAppTarget,
		Endpoints: endpoints,
	}, nil
}

// Gets endpoint for the container app service
func (at *containerAppTarget) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	containerAppOptions := containerapps.ContainerAppOptions{
		ApiVersion: serviceConfig.ApiVersion,
	}

	if ingressConfig, err := at.containerAppService.GetIngressConfiguration(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
		&containerAppOptions,
	); err != nil {
		return nil, fmt.Errorf("fetching service properties: %w", err)
	} else {
		endpoints := make([]string, len(ingressConfig.HostNames))
		for idx, hostName := range ingressConfig.HostNames {
			endpoints[idx] = fmt.Sprintf("https://%s/", hostName)
		}

		return endpoints, nil
	}
}

func (at *containerAppTarget) validateTargetResource(
	targetResource *environment.TargetResource,
) error {
	if targetResource.ResourceGroupName() == "" {
		return fmt.Errorf("missing resource group name: %s", targetResource.ResourceGroupName())
	}

	if targetResource.ResourceType() != "" {
		if err := checkResourceType(targetResource, azapi.AzureResourceTypeContainerApp); err != nil {
			return err
		}
	}

	return nil
}

func (at *containerAppTarget) addPreProvisionChecks(_ context.Context, serviceConfig *ServiceConfig) error {
	// Attempt to retrieve the target resource for the current service
	// This allows the resource deployment to detect whether or not to pull existing container image during
	// provision operation to avoid resetting the container app back to a default image
	return serviceConfig.Project.AddHandler("preprovision", func(ctx context.Context, args ProjectLifecycleEventArgs) error {
		exists := false

		// Check if the target resource already exists
		targetResource, err := at.resourceManager.GetTargetResource(ctx, at.env.GetSubscriptionId(), serviceConfig)
		if targetResource != nil && err == nil {
			exists = true
		}

		at.env.SetServiceProperty(serviceConfig.Name, "RESOURCE_EXISTS", strconv.FormatBool(exists))
		return at.envManager.Save(ctx, at.env)
	})
}
