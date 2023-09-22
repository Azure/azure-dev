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

type appServiceTarget struct {
	env               *environment.Environment
	cli               azcli.AzCli
	containerHelper   *ContainerHelper
	appServiceService appService.AppServiceService
}

// NewAppServiceTarget creates a new instance of the AppServiceTarget
func NewAppServiceTarget(
	env *environment.Environment,
	azCli azcli.AzCli,
	containerHelper *ContainerHelper,
	appServiceService appService.AppServiceService,
) ServiceTarget {

	return &appServiceTarget{
		env:               env,
		cli:               azCli,
		containerHelper:   containerHelper,
		appServiceService: appServiceService,
	}
}

// Gets the required external tools
func (st *appServiceTarget) RequiredExternalTools(context.Context) []tools.ExternalTool {
	return []tools.ExternalTool{}
}

// Initializes the AppService target
func (st *appServiceTarget) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

// Prepares a zip archive from the specified build output
func (st *appServiceTarget) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServicePackageResult, ServiceProgress]) {
			// check if serviceConfig.Docker has valid values
			if isDockerDeployment(serviceConfig) {
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
func (st *appServiceTarget) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
	targetResource *environment.TargetResource,
) *async.TaskWithProgress[*ServiceDeployResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServiceDeployResult, ServiceProgress]) {
			if err := st.validateTargetResource(ctx, serviceConfig, targetResource); err != nil {
				task.SetError(fmt.Errorf("validating target resource: %w", err))
				return
			}

			if isDockerDeployment(serviceConfig) {
				sdr, err := st.containerDeploy(ctx, serviceConfig, packageOutput, targetResource, task)
				if err != nil {
					return
				}
				task.SetResult(sdr)

			} else {
				// Deploy the zip archive
				sdr, err := st.zipDeploy(ctx, packageOutput, task, targetResource, serviceConfig)

				if err != nil {
					task.SetError(err)
					return
				}

				task.SetResult(sdr)
			}
		},
	)
}

func (st *appServiceTarget) containerDeploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
	targetResource *environment.TargetResource,
	task *async.TaskContextWithProgress[*ServiceDeployResult, ServiceProgress]) (*ServiceDeployResult, error) {
	containerDeployTask := st.containerHelper.Deploy(ctx, serviceConfig, packageOutput, targetResource)
	syncProgress(task, containerDeployTask.Progress())

	_, err := containerDeployTask.Await()
	if err != nil {
		return nil, err
	}

	imageName := st.env.GetServiceProperty(serviceConfig.Name, "IMAGE_NAME")
	task.SetProgress(NewServiceProgress("Updating app service container"))
	st.appServiceService.AddRevision(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
		imageName,
	)

	endpoints, err := st.Endpoints(ctx, serviceConfig, targetResource)
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

func (st *appServiceTarget) zipDeploy(
	ctx context.Context,
	packageOutput *ServicePackageResult,
	task *async.TaskContextWithProgress[*ServiceDeployResult, ServiceProgress],
	targetResource *environment.TargetResource,
	serviceConfig *ServiceConfig) (*ServiceDeployResult, error) {
	zipFile, err := os.Open(packageOutput.PackagePath)
	if err != nil {
		return nil, fmt.Errorf("failed reading deployment zip file: %w", err)
	}

	defer os.Remove(packageOutput.PackagePath)
	defer zipFile.Close()

	task.SetProgress(NewServiceProgress("Uploading deployment package"))
	res, err := st.cli.DeployAppServiceZip(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
		zipFile,
	)
	if err != nil {
		return nil, fmt.Errorf("deploying service %s: %w", serviceConfig.Name, err)
	}

	task.SetProgress(NewServiceProgress("Fetching endpoints for app service"))
	endpoints, err := st.Endpoints(ctx, serviceConfig, targetResource)
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
		*res,
		endpoints,
	)
	sdr.Package = packageOutput

	return sdr, nil
}

// Gets the exposed endpoints for the App Service
func (st *appServiceTarget) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	appServiceProperties, err := st.cli.GetAppServiceProperties(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
	)
	if err != nil {
		return nil, fmt.Errorf("fetching service properties: %w", err)
	}

	endpoints := make([]string, len(appServiceProperties.HostNames))
	for idx, hostName := range appServiceProperties.HostNames {
		endpoints[idx] = fmt.Sprintf("https://%s/", hostName)
	}

	return endpoints, nil
}

func (st *appServiceTarget) validateTargetResource(
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

func isDockerDeployment(serviceConfig *ServiceConfig) bool {
	return serviceConfig.Docker.Path != ""
}
