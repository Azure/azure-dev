// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/benbjohnson/clock"
)

type containerAppTarget struct {
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
	if err := at.cli.LoginAcr(ctx, at.commandRunner, at.env.GetSubscriptionId(), loginServer); err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("logging into registry '%s': %w", loginServer, err)
	}

	imageTag, err := at.generateImageTag()
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
	at.env.SetServiceProperty(at.config.Name, "IMAGE_NAME", fullTag)

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
		&mutedConsole{
			parentConsole: at.console,
		}, // make provision output silence
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

		if err := checkResourceType(at.resource); err != nil {
			return ServiceDeploymentResult{}, err
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

func (at *containerAppTarget) generateImageTag() (string, error) {
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
		at.clock.Now().Unix(),
	), nil
}

// NewContainerAppTarget creates the container app service target.
//
// The target resource can be partially filled with only ResourceGroupName, since container apps
// can be provisioned during deployment.
func NewContainerAppTarget(
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
		if err := checkResourceType(resource); err != nil {
			return nil, err
		}
	}

	return &containerAppTarget{
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

func checkResourceType(resource *environment.TargetResource) error {
	if !strings.EqualFold(resource.ResourceType(), string(infra.AzureResourceTypeContainerApp)) {
		return resourceTypeMismatchError(
			resource.ResourceName(),
			resource.ResourceType(),
			infra.AzureResourceTypeContainerApp,
		)
	}

	return nil
}

// A console implementation which output goes only to logs
// This is used to prevent or stop actions using the terminal output, for
// example, when calling provision during deploying a service.
type mutedConsole struct {
	parentConsole input.Console
}

// Sets the underlying writer for output the console or
// if writer is nil, sets it back to the default writer.
func (sc *mutedConsole) SetWriter(writer io.Writer) {
	log.Println("tried to set writer for silent console is a no-op action")
}

func (sc *mutedConsole) GetFormatter() output.Formatter {
	return nil
}

func (sc *mutedConsole) IsUnformatted() bool {
	return true
}

// Prints out a message to the underlying console write
func (sc *mutedConsole) Message(ctx context.Context, message string) {
	log.Println(message)
}

func (sc *mutedConsole) MessageUxItem(ctx context.Context, item ux.UxItem) {
	sc.Message(ctx, item.ToString(""))
}

func (sc *mutedConsole) ShowSpinner(ctx context.Context, title string, format input.SpinnerUxType) {
	log.Printf("request to show spinner on silent console with message: %s", title)
}

func (sc *mutedConsole) StopSpinner(ctx context.Context, lastMessage string, format input.SpinnerUxType) {
	log.Printf("request to stop spinner on silent console with message: %s", lastMessage)
}

// Use parent console for input
func (sc *mutedConsole) Prompt(ctx context.Context, options input.ConsoleOptions) (string, error) {
	return sc.parentConsole.Prompt(ctx, options)
}

// Use parent console for input
func (sc *mutedConsole) Select(ctx context.Context, options input.ConsoleOptions) (int, error) {
	return sc.parentConsole.Select(ctx, options)
}

// Use parent console for input
func (sc *mutedConsole) Confirm(ctx context.Context, options input.ConsoleOptions) (bool, error) {
	return sc.parentConsole.Confirm(ctx, options)
}

func (sc *mutedConsole) GetWriter() io.Writer {
	return nil
}

func (sc *mutedConsole) Handles() input.ConsoleHandles {
	return sc.parentConsole.Handles()
}
