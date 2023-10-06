// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/appservice"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

// functionAppTarget specifies an Azure Function to deploy to.
// Implements `project.ServiceTarget`
type functionAppTarget struct {
	env *environment.Environment
	cli azcli.AzCli
	containerHelper   *ContainerHelper
	appServiceService appService.AppServiceService
}

// NewFunctionAppTarget creates a new instance of the Function App target
func NewFunctionAppTarget(
	env *environment.Environment,
	azCli azcli.AzCli,
	containerHelper *ContainerHelper,
	appServiceService appService.AppServiceService,
) ServiceTarget {
	return &functionAppTarget{
		env: env,
		cli: azCli,
		containerHelper:   containerHelper,
		appServiceService: appServiceService,
	}
}

// Gets the required external tools for the Function app
func (f *functionAppTarget) RequiredExternalTools(context.Context) []tools.ExternalTool {
	return []tools.ExternalTool{}
}

// Initializes the function app target
func (f *functionAppTarget) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

// Prepares a zip archive from the specified build output
func (f *functionAppTarget) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServicePackageResult, ServiceProgress]) {
			if f.isDockerDeployment(serviceConfig) {
				task.SetResult(packageOutput)
				return
			}

			task.SetProgress(NewServiceProgress("Compressing deployment artifacts"))
			zipFilePath, err := createDeployableZip(
				serviceConfig.Project.Name,
				serviceConfig.Name,
				packageOutput.PackagePath,
			)
			if err != nil {
				task.SetError(err)
				return
			}

			task.SetResult(&ServicePackageResult{
				Build:       packageOutput.Build,
				PackagePath: zipFilePath,
			})
		},
	)
}

// Deploys the prepared zip archive using Zip deploy to the Azure App Service resource
func (f *functionAppTarget) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
	targetResource *environment.TargetResource,
) *async.TaskWithProgress[*ServiceDeployResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServiceDeployResult, ServiceProgress]) {
			if err := f.validateTargetResource(ctx, serviceConfig, targetResource); err != nil {
				task.SetError(fmt.Errorf("validating target resource: %w", err))
				return
			}

			if f.isDockerDeployment(serviceConfig) {
				sdr, err := f.containerDeploy(ctx, serviceConfig, packageOutput, targetResource, task)
				if err != nil {
					task.SetError(err)
					return
				}
				task.SetResult(sdr)
			} else {
				sdr, err := f.zipDeploy(ctx, packageOutput, task, targetResource, serviceConfig)
				if err != nil {
					task.SetError(err)
					return
				}
				task.SetResult(sdr)
			}
		},
	)
}

// Gets the exposed endpoints for the Function App
func (f *functionAppTarget) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	// TODO(azure/azure-dev#670) Implement this. For now we just return an empty set of endpoints and
	// a nil error.  In `deploy` we just loop over the endpoint array and print any endpoints, so returning
	// an empty array and nil error will mean "no endpoints".
	if props, err := f.cli.GetFunctionAppProperties(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName()); err != nil {
		return nil, fmt.Errorf("fetching service properties: %w", err)
	} else {
		endpoints := make([]string, len(props.HostNames))
		for idx, hostName := range props.HostNames {
			endpoints[idx] = fmt.Sprintf("https://%s/", hostName)
		}

		return endpoints, nil
	}
}

func (f *functionAppTarget) validateTargetResource(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) error {
	if !strings.EqualFold(targetResource.ResourceType(), string(infra.AzureResourceTypeWebSite)) {
		return resourceTypeMismatchError(
			targetResource.ResourceName(),
			targetResource.ResourceType(),
			infra.AzureResourceTypeWebSite,
		)
	}

	return nil
}

func (f *functionAppTarget) isDockerDeployment(serviceConfig *ServiceConfig) bool {
	return serviceConfig.Docker.Path != ""
}

func (f *functionAppTarget) containerDeploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
	targetResource *environment.TargetResource,
	task *async.TaskContextWithProgress[*ServiceDeployResult, ServiceProgress]) (*ServiceDeployResult, error) {
	containerDeployTask := f.containerHelper.Deploy(ctx, serviceConfig, packageOutput, targetResource)
	syncProgress(task, containerDeployTask.Progress())

	_, err := containerDeployTask.Await()
	if err != nil {
		return nil, err
	}

	imageName := f.env.GetServiceProperty(serviceConfig.Name, "IMAGE_NAME")
	task.SetProgress(NewServiceProgress("Updating app service container"))
	f.appServiceService.AddRevision(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
		imageName,
	)

	endpoints, err := f.Endpoints(ctx, serviceConfig, targetResource)
	if err != nil {
		return nil, err
	}

	sdr := NewServiceDeployResult(
		azure.WebsiteRID(
			targetResource.SubscriptionId(),
			targetResource.ResourceGroupName(),
			targetResource.ResourceName(),
		),
		AppServiceTarget,
		// TODO: Populate this string properly
		"",
		endpoints,
	)
	sdr.Package = packageOutput

	return sdr, nil
}

func (f *functionAppTarget) zipDeploy(
	ctx context.Context,
	packageOutput *ServicePackageResult,
	task *async.TaskContextWithProgress[*ServiceDeployResult, ServiceProgress],
	targetResource *environment.TargetResource,
	serviceConfig *ServiceConfig,
) (*ServiceDeployResult, error) {
	zipFile, err := os.Open(packageOutput.PackagePath)
	if err != nil {
		return nil, err
	}

	defer os.Remove(packageOutput.PackagePath)
	defer zipFile.Close()

	task.SetProgress(NewServiceProgress("Uploading deployment package"))
	res, err := f.cli.DeployFunctionAppUsingZipFile(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
		zipFile,
	)
	if err != nil {
		return nil, err
	}

	task.SetProgress(NewServiceProgress("Fetching endpoints for function app"))
	endpoints, err := f.Endpoints(ctx, serviceConfig, targetResource)
	if err != nil {
		return nil, err
	}

	sdr := NewServiceDeployResult(
		azure.WebsiteRID(
			targetResource.SubscriptionId(),
			targetResource.ResourceGroupName(),
			targetResource.ResourceName(),
		),
		AzureFunctionTarget,
		*res,
		endpoints,
	)
	sdr.Package = packageOutput

	return sdr, nil
}