// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/benbjohnson/clock"
	"log"
	"strings"
)

type springAppTarget struct {
	config        *ServiceConfig
	env           *environment.Environment
	resource      *environment.TargetResource
	cli           azcli.AzCli
	docker        *docker.Docker
	console       input.Console
	commandRunner exec.CommandRunner

	// Standard time library clock, unless mocked in tests
	clock clock.Clock
}

func (at *springAppTarget) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{at.docker}
}

func (at *springAppTarget) Deploy(
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
	if err := at.cli.LoginAcr(ctx, at.commandRunner, at.env.GetSubscriptionId(), loginServer); err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("logging into registry '%s': %w", loginServer, err)
	}

	imageTag, err := at.generateImageTag(at.clock.Now().Unix())
	if err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("generating image tag: %w", err)
	}

	fullTag := fmt.Sprintf(
		"%s/%s",
		loginServer,
		imageTag,
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
	at.env.Values[fmt.Sprintf("SERVICE_%s_IMAGE_NAME", strings.ReplaceAll(strings.ToUpper(at.config.Name), "-", "_"))] = imageTag

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
		&silentConsole{}, // make provision output silence
		at.commandRunner,
	)
	if err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("creating provisioning manager: %w", err)
	}

	progress <- "Creating deployment template"
	deploymentPlan, err := infraManager.Plan(ctx)
	if err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("planning provisioning: %w", err)
	}

	progress <- "Updating spring app image reference"
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

	if at.resource.ResourceName() == "" {
		targetResource, err := at.config.GetServiceResource(ctx, at.resource.ResourceGroupName(), at.env, at.cli, "deploy")
		if err != nil {
			return ServiceDeploymentResult{}, err
		}

		// Fill in the target resource
		at.resource = environment.NewTargetResource(
			at.env.GetSubscriptionId(),
			at.resource.ResourceGroupName(),
			targetResource.Name,
			targetResource.Type,
		)

		if err := internal.CheckResourceType(at.resource, infra.AzureResourceTypeSpringApp); err != nil {
			return ServiceDeploymentResult{}, err
		}
	}

	progress <- "Fetching endpoints for spring app service"
	endpoints, err := at.Endpoints(ctx)
	if err != nil {
		return ServiceDeploymentResult{}, err
	}

	return ServiceDeploymentResult{
		TargetResourceId: azure.SpringAppRID(
			at.env.GetSubscriptionId(),
			at.resource.ResourceGroupName(),
			at.resource.ResourceName(),
		),
		Kind:      SpringAppTarget,
		Details:   deployResult,
		Endpoints: endpoints,
	}, nil
}

func (at *springAppTarget) Endpoints(ctx context.Context) ([]string, error) {
	if springAppProperties, err := at.cli.GetSpringAppProperties(
		ctx, at.env.GetSubscriptionId(),
		at.resource.ResourceGroupName(),
		at.resource.ResourceName(),
	); err != nil {
		return nil, fmt.Errorf("fetching service properties: %w", err)
	} else {
		endpoints := make([]string, len(springAppProperties.HostNames))
		for idx, hostName := range springAppProperties.HostNames {
			endpoints[idx] = fmt.Sprintf("https://%s/", getASAEndpoint(hostName))
		}

		return endpoints, nil
	}
}

func (at *springAppTarget) generateImageTag(timestamp int64) (string, error) {
	configuredTag, err := at.config.Docker.Tag.Envsubst(at.env.Getenv)
	if err != nil {
		return "", err
	}

	if configuredTag != "" {
		return configuredTag, nil
	}

	return fmt.Sprintf("%s/%s-%s:azdev-deploy-%d",
		strings.ToLower(at.config.Project.Name),
		strings.ToLower(at.config.Name),
		strings.ToLower(at.env.GetEnvName()),
		timestamp,
	), nil
}

func getASAEndpoint(hostName string) string {
	index := strings.IndexRune(hostName, '.')
	return hostName[0:index] + "-default" + hostName[index:]
}

// NewSpringAppTarget creates the spring app service target.
//
// The target resource can be partially filled with only ResourceGroupName, since spring apps
// can be provisioned during deployment.
func NewSpringAppTarget(
	config *ServiceConfig,
	env *environment.Environment,
	resource *environment.TargetResource,
	azCli azcli.AzCli,
	docker *docker.Docker,
	console input.Console,
	commandRunner exec.CommandRunner,
) (ServiceTarget, error) {
	if resource.ResourceGroupName() == "" {
		return nil, fmt.Errorf("missing resource group name: %s", resource.ResourceGroupName())
	}

	if resource.ResourceType() != "" {
		if err := internal.CheckResourceType(resource, infra.AzureResourceTypeSpringApp); err != nil {
			return nil, err
		}
	}

	return &springAppTarget{
		config:        config,
		env:           env,
		resource:      resource,
		cli:           azCli,
		docker:        docker,
		console:       console,
		commandRunner: commandRunner,
		clock:         clock.New(),
	}, nil
}
