// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
)

type containerAppTarget struct {
	config   *ServiceConfig
	env      *environment.Environment
	resource *environment.TargetResource
	cli      azcli.AzCli
	docker   *docker.Docker
	console  input.Console
}

func (at *containerAppTarget) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{at.docker}
}

func (at *containerAppTarget) Deploy(
	ctx context.Context,
	azdCtx *azdcontext.AzdContext,
	path string,
	progress chan<- string,
) (ServiceDeploymentResult, error) {
	// If the infra module has not been specified default to a module with the same name as the service.
	if strings.TrimSpace(at.config.Infra.Module) == "" {
		at.config.Infra.Module = at.config.Module
	}
	if strings.TrimSpace(at.config.Infra.Module) == "" {
		at.config.Infra.Module = at.config.Name
	}

	// Login to container registry.
	loginServer, has := at.env.Values[environment.ContainerRegistryEndpointEnvVarName]
	if !has {
		return ServiceDeploymentResult{}, fmt.Errorf(
			"could not determine container registry endpoint, ensure %s is set as an output of your infrastructure",
			environment.ContainerRegistryEndpointEnvVarName,
		)
	}

	log.Printf("logging into registry %s", loginServer)

	progress <- "Logging into container registry"
	if err := at.cli.LoginAcr(ctx, at.env.GetSubscriptionId(), loginServer); err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("logging into registry '%s': %w", loginServer, err)
	}

	fullTag := fmt.Sprintf(
		"%s/%s/%s:azdev-deploy-%d",
		loginServer,
		at.resource.ResourceName(),
		at.resource.ResourceName(),
		time.Now().Unix(),
	)

	// Tag image.
	log.Printf("tagging image %s as %s", path, fullTag)
	progress <- "Tagging image"
	if err := at.docker.Tag(ctx, at.config.Path(), path, fullTag); err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("tagging image: %w", err)
	}

	log.Printf("pushing %s to registry", fullTag)

	// Push image.
	progress <- "Pushing container image"
	if err := at.docker.Push(ctx, at.config.Path(), fullTag); err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("pushing image: %w", err)
	}

	log.Printf("writing image name to environment")

	// Save the name of the image we pushed into the environment with a well known key.
	at.env.Values[fmt.Sprintf("SERVICE_%s_IMAGE_NAME", strings.ToUpper(at.config.Name))] = fullTag

	if err := at.env.Save(); err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("saving image name to environment: %w", err)
	}

	infraManager, err := provisioning.NewManager(
		ctx,
		at.env,
		at.config.Project.Path,
		at.config.Infra,
		at.console.IsUnformatted(),
		at.cli,
	)
	if err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("creating provisioning manager: %w", err)
	}

	progress <- "Creating deployment template"
	deploymentPlan, err := infraManager.Plan(ctx)
	if err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("planning provisioning: %w", err)
	}

	progress <- "Updating container app image reference"
	deploymentName := fmt.Sprintf("%s-%s", at.env.GetEnvName(), at.config.Name)
	scope := infra.NewResourceGroupScope(
		at.cli,
		at.env.GetSubscriptionId(),
		at.resource.ResourceGroupName(),
		deploymentName,
	)
	deployResult, err := infraManager.Deploy(ctx, deploymentPlan, scope)

	if err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("provisioning infrastructure for app deployment: %w", err)
	}

	if len(deployResult.Deployment.Outputs) > 0 {
		log.Printf("saving %d deployment outputs", len(deployResult.Deployment.Outputs))
		if err := provisioning.UpdateEnvironment(at.env, deployResult.Deployment.Outputs); err != nil {
			return ServiceDeploymentResult{}, fmt.Errorf("saving outputs to environment: %w", err)
		}
	}

	progress <- "Fetching endpoints for container app service"
	endpoints, err := at.Endpoints(ctx)
	if err != nil {
		return ServiceDeploymentResult{}, err
	}

	return ServiceDeploymentResult{
		TargetResourceId: azure.ContainerAppRID(
			at.env.GetSubscriptionId(),
			at.resource.ResourceGroupName(),
			at.resource.ResourceName(),
		),
		Kind:      ContainerAppTarget,
		Details:   deployResult,
		Endpoints: endpoints,
	}, nil
}

func (at *containerAppTarget) Endpoints(ctx context.Context) ([]string, error) {
	if containerAppProperties, err := at.cli.GetContainerAppProperties(
		ctx, at.env.GetSubscriptionId(),
		at.resource.ResourceGroupName(),
		at.resource.ResourceName(),
	); err != nil {
		return nil, fmt.Errorf("fetching service properties: %w", err)
	} else {
		endpoints := make([]string, len(containerAppProperties.HostNames))
		for idx, hostName := range containerAppProperties.HostNames {
			endpoints[idx] = fmt.Sprintf("https://%s/", hostName)
		}

		return endpoints, nil
	}
}

func NewContainerAppTarget(
	config *ServiceConfig,
	env *environment.Environment,
	resource *environment.TargetResource,
	azCli azcli.AzCli,
	docker *docker.Docker,
	console input.Console,
) (ServiceTarget, error) {
	if resource.ResourceType() != string(infra.AzureResourceTypeContainerApp) {
		return nil, resourceTypeMismatchError(
			resource.ResourceName(),
			resource.ResourceType(),
			infra.AzureResourceTypeContainerApp,
		)
	}

	return &containerAppTarget{
		config:   config,
		env:      env,
		resource: resource,
		cli:      azCli,
		docker:   docker,
		console:  console,
	}, nil
}
