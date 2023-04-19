// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"io"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

type containerAppTarget struct {
	env                        *environment.Environment
	cli                        azcli.AzCli
	console                    input.Console
	commandRunner              exec.CommandRunner
	accountManager             account.Manager
	serviceManager             ServiceManager
	resourceManager            ResourceManager
	containerHelper            *ContainerHelper
	containerAppService        azcli.ContainerAppService
	alphaFeatureManager        *alpha.FeatureManager
	userProfileService         *azcli.UserProfileService
	subscriptionTenantResolver account.SubscriptionTenantResolver
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
	userProfileService *azcli.UserProfileService,
	subscriptionTenantResolver account.SubscriptionTenantResolver,
	containerHelper *ContainerHelper,
	containerAppService azcli.ContainerAppService,
	alphaFeatureManager *alpha.FeatureManager,
) ServiceTarget {
	return &containerAppTarget{
		env:                        env,
		accountManager:             accountManager,
		serviceManager:             serviceManager,
		resourceManager:            resourceManager,
		cli:                        azCli,
		console:                    console,
		commandRunner:              commandRunner,
		containerHelper:            containerHelper,
		containerAppService:        containerAppService,
		alphaFeatureManager:        alphaFeatureManager,
		userProfileService:         userProfileService,
		subscriptionTenantResolver: subscriptionTenantResolver,
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

			// Login, tag & push container image to ACR
			containerDeployTask := at.containerHelper.Deploy(ctx, serviceConfig, packageOutput, targetResource)
			syncProgress(task, containerDeployTask.Progress())

			deployResult, err := containerDeployTask.Await()
			if err != nil {
				task.SetError(err)
				return
			}

			imageName := at.env.GetServiceProperty(serviceConfig.Name, "IMAGE_NAME")
			task.SetProgress(NewServiceProgress("Updating container app revision"))
			err = at.containerAppService.AddRevision(
				ctx,
				targetResource.SubscriptionId(),
				targetResource.ResourceGroupName(),
				targetResource.ResourceName(),
				imageName,
			)
			if err != nil {
				task.SetError(fmt.Errorf("updating container app service: %w", err))
				return
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
	if containerAppProperties, err := at.containerAppService.GetAppProperties(
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
type MutedConsole struct {
	ParentConsole input.Console
}

// Sets the underlying writer for output the console or
// if writer is nil, sets it back to the default writer.
func (sc *MutedConsole) SetWriter(writer io.Writer) {
	log.Println("tried to set writer for silent console is a no-op action")
}

func (sc *MutedConsole) GetFormatter() output.Formatter {
	return nil
}

func (sc *MutedConsole) IsUnformatted() bool {
	return true
}

// Prints out a message to the underlying console write
func (sc *MutedConsole) Message(ctx context.Context, message string) {
	log.Println(message)
}

func (sc *MutedConsole) MessageUxItem(ctx context.Context, item ux.UxItem) {
	sc.Message(ctx, item.ToString(""))
}

func (sc *MutedConsole) ShowSpinner(ctx context.Context, title string, format input.SpinnerUxType) {
	log.Printf("request to show spinner on silent console with message: %s", title)
}

func (sc *MutedConsole) StopSpinner(ctx context.Context, lastMessage string, format input.SpinnerUxType) {
	log.Printf("request to stop spinner on silent console with message: %s", lastMessage)
}

func (sc *MutedConsole) IsSpinnerRunning(ctx context.Context) bool {
	return false
}

// Use parent console for input
func (sc *MutedConsole) Prompt(ctx context.Context, options input.ConsoleOptions) (string, error) {
	return sc.ParentConsole.Prompt(ctx, options)
}

// Use parent console for input
func (sc *MutedConsole) Select(ctx context.Context, options input.ConsoleOptions) (int, error) {
	return sc.ParentConsole.Select(ctx, options)
}

// Use parent console for input
func (sc *MutedConsole) Confirm(ctx context.Context, options input.ConsoleOptions) (bool, error) {
	return sc.ParentConsole.Confirm(ctx, options)
}

func (sc *MutedConsole) GetWriter() io.Writer {
	return nil
}

func (sc *MutedConsole) Handles() input.ConsoleHandles {
	return sc.ParentConsole.Handles()
}
