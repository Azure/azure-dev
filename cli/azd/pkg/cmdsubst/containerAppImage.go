// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmdsubst

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/containerapps"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

// ContainerAppImageCommandName is the name of the command that can be used to fetch the current image for a container in
// a container app. It expects two arguments, the first is the name of a service in azure.yaml and the second is the name
// of the container in the template for that container app you want to pull the image id from.
//
// If the container app has not yet been created, containerAppImage returns an empty string.
//
// The expected use of this command is in main.parameters.json files, like this:
//
//	"imageName": {
//	  "value": "${SERVICE_WEB_IMAGE_NAME:=$(containerAppImage web main)}"
//	}
//
// Here, we use the value of SERVICE_WEB_IMAGE_NAME if it is set (this value is updated in the .env file every time
// `azd deploy` is run) and if it is not set (for example, in CI where this value is not persisted across runs), the
// current value is pulled from the live Container App resource.
//
// This can be useful to ensure that the container app image is not reset to an initial value.
const ContainerAppImageCommandName string = "containerAppImage"

type ContainerAppImageCommandExecutor struct {
	resourceManager      project.ResourceManager
	containerAppsService containerapps.ContainerAppService
	subscriptionId       string
	services             map[string]*project.ServiceConfig
}

func NewContainerAppImageExecutor(
	resourceManager project.ResourceManager,
	containerAppsService containerapps.ContainerAppService,
	subscriptionId string,
	services map[string]*project.ServiceConfig) *ContainerAppImageCommandExecutor {
	return &ContainerAppImageCommandExecutor{
		resourceManager:      resourceManager,
		containerAppsService: containerAppsService,
		subscriptionId:       subscriptionId,
		services:             services,
	}
}

func (e *ContainerAppImageCommandExecutor) Run(
	ctx context.Context,
	commandName string,
	args []string,
) (bool, string, error) {
	if commandName != ContainerAppImageCommandName {
		return false, "", nil
	}

	// We expect two arguments, the name of the service to target and the name of the container to pull the image of
	if len(args) != 2 {
		return false, "", fmt.Errorf("%s expected one two arguments but got %d", ContainerAppImageCommandName, len(args))
	}

	serviceName := args[0]
	containerName := args[1]

	if ctx == nil || e.resourceManager == nil {
		// Should never happen really...
		return false, "", fmt.Errorf("missing context information for %s command", SecretOrRandomPasswordCommandName)
	}

	service, has := e.services[args[0]]
	if !has {
		return false, "", fmt.Errorf("%s no service %s defined in azure.yaml", ContainerAppImageCommandName, serviceName)
	}

	// If the target has not been created yet, return an empty image.
	targetResource, err := e.resourceManager.GetTargetResource(ctx, e.subscriptionId, service)
	if err != nil {
		return true, "", nil
	}

	if !strings.EqualFold(targetResource.ResourceType(), string(infra.AzureResourceTypeContainerApp)) {
		return false, "", fmt.Errorf("service %s does not target container apps", serviceName)
	}

	app, err := e.containerAppsService.GetContainerApp(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
	)
	if err != nil {
		return false, "", fmt.Errorf("reading container app properties: %w", err)
	}

	for _, container := range app.Properties.Template.Containers {
		if *container.Name == containerName {
			return true, *container.Image, nil
		}
	}

	return false, "", fmt.Errorf(
		"service %s does not container a container named %s in its template", serviceName, containerName)
}
