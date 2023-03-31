// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

type containerAppTarget struct {
	env             *environment.Environment
	cli             azcli.AzCli
	console         input.Console
	commandRunner   exec.CommandRunner
	accountManager  account.Manager
	serviceManager  ServiceManager
	resourceManager ResourceManager
	containerHelper *ContainerHelper
}

// NewContainerAppTarget creates the container app service target.
//
// The target resource can be partially filled with only ResourceGroupName, since container apps
// can be provisioned during deployment.
func NewContainerAppTarget(
	env *environment.Environment,
	azCli azcli.AzCli,
	console input.Console,
	commandRunner exec.CommandRunner,
	accountManager account.Manager,
	serviceManager ServiceManager,
	resourceManager ResourceManager,
	containerHelper *ContainerHelper,
) ServiceTarget {
	return &containerAppTarget{
		env:             env,
		accountManager:  accountManager,
		serviceManager:  serviceManager,
		resourceManager: resourceManager,
		cli:             azCli,
		console:         console,
		commandRunner:   commandRunner,
		containerHelper: containerHelper,
	}
}

// Gets the required external tools
func (at *containerAppTarget) RequiredExternalTools(ctx context.Context) []tools.ExternalTool {
	return at.containerHelper.RequiredExternalTools(ctx)
}

// Initializes the Container App target
func (at *containerAppTarget) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

// Prepares and tags the container image from the build output based on the specified service configuration
func (at *containerAppTarget) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServicePackageResult, ServiceProgress]) {
			task.SetResult(packageOutput)
		},
	)
}

// Deploys service container images to ACR and provisions the container app service.
func (at *containerAppTarget) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
	targetResource *environment.TargetResource,
) *async.TaskWithProgress[*ServiceDeployResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServiceDeployResult, ServiceProgress]) {
			if err := at.validateTargetResource(ctx, serviceConfig, targetResource); err != nil {
				task.SetError(fmt.Errorf("validating target resource: %w", err))
				return
			}

			// If the infra module has not been specified default to a module with the same name as the service.
			if strings.TrimSpace(serviceConfig.Infra.Module) == "" {
				serviceConfig.Infra.Module = serviceConfig.Module
			}
			if strings.TrimSpace(serviceConfig.Infra.Module) == "" {
				serviceConfig.Infra.Module = serviceConfig.Name
			}

			// Login, tag & push container image to ACR
			containerDeployTask := at.containerHelper.Deploy(ctx, serviceConfig, packageOutput, targetResource)
			syncProgress(task, containerDeployTask.Progress())

			_, err := containerDeployTask.Await()
			if err != nil {
				task.SetError(err)
				return
			}

			infraManager, err := provisioning.NewManager(
				ctx,
				at.env,
				serviceConfig.Project.Path,
				serviceConfig.Infra,
				at.console.IsUnformatted(),
				at.cli,
				&mutedConsole{
					parentConsole: at.console,
				}, // make provision output silence
				at.commandRunner,
				at.accountManager,
			)
			if err != nil {
				task.SetError(fmt.Errorf("creating provisioning manager: %w", err))
				return
			}

			task.SetProgress(NewServiceProgress("Creating deployment template"))
			deploymentPlan, err := infraManager.Plan(ctx)
			if err != nil {
				task.SetError(fmt.Errorf("planning provisioning: %w", err))
				return
			}

			task.SetProgress(NewServiceProgress("Updating container app image reference"))
			deploymentName := fmt.Sprintf("%s-%s", at.env.GetEnvName(), serviceConfig.Name)
			scope := infra.NewResourceGroupScope(
				at.cli,
				targetResource.SubscriptionId(),
				targetResource.ResourceGroupName(),
				deploymentName,
			)
			deployResult, err := infraManager.Deploy(ctx, deploymentPlan, scope)

			if err != nil {
				task.SetError(fmt.Errorf("provisioning infrastructure for app deployment: %w", err))
				return
			}

			if len(deployResult.Deployment.Outputs) > 0 {
				log.Printf("saving %d deployment outputs", len(deployResult.Deployment.Outputs))
				if err := provisioning.UpdateEnvironment(at.env, deployResult.Deployment.Outputs); err != nil {
					task.SetError(fmt.Errorf("saving outputs to environment: %w", err))
					return
				}
			}

			if targetResource.ResourceName() == "" {
				azureResource, err := at.resourceManager.GetServiceResource(
					ctx,
					targetResource.SubscriptionId(),
					targetResource.ResourceGroupName(),
					serviceConfig,
					"deploy",
				)
				if err != nil {
					task.SetError(err)
					return
				}

				// Fill in the target resource
				targetResource = environment.NewTargetResource(
					targetResource.SubscriptionId(),
					targetResource.ResourceGroupName(),
					azureResource.Name,
					azureResource.Type,
				)

				if err := checkResourceType(targetResource, infra.AzureResourceTypeContainerApp); err != nil {
					task.SetError(err)
					return
				}
			}

			task.SetProgress(NewServiceProgress("Fetching endpoints for container app service"))
			endpoints, err := at.Endpoints(ctx, serviceConfig, targetResource)
			if err != nil {
				task.SetError(err)
				return
			}

			task.SetResult(&ServiceDeployResult{
				Package: packageOutput,
				TargetResourceId: azure.ContainerAppRID(
					targetResource.SubscriptionId(),
					targetResource.ResourceGroupName(),
					targetResource.ResourceName(),
				),
				Kind:      ContainerAppTarget,
				Details:   deployResult,
				Endpoints: endpoints,
			})
		},
	)
}

// Gets endpoint for the container app service
func (at *containerAppTarget) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	if containerAppProperties, err := at.cli.GetContainerAppProperties(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
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

func (at *containerAppTarget) validateTargetResource(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) error {
	if targetResource.ResourceGroupName() == "" {
		return fmt.Errorf("missing resource group name: %s", targetResource.ResourceGroupName())
	}

	if targetResource.ResourceType() != "" {
		if err := checkResourceType(targetResource, infra.AzureResourceTypeContainerApp); err != nil {
			return err
		}
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

func (sc *mutedConsole) IsSpinnerRunning(ctx context.Context) bool {
	return false
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
