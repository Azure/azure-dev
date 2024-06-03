// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/swa"
)

// TODO: Enhance for multi-environment support
// https://github.com/Azure/azure-dev/issues/1152
const DefaultStaticWebAppEnvironmentName = "default"

type staticWebAppTarget struct {
	env *environment.Environment
	cli azcli.AzCli
	swa swa.SwaCli
}

// NewStaticWebAppTarget creates a new instance of the Static Web App target
func NewStaticWebAppTarget(
	env *environment.Environment,
	azCli azcli.AzCli,
	swaCli swa.SwaCli,
) ServiceTarget {
	return &staticWebAppTarget{
		env: env,
		cli: azCli,
		swa: swaCli,
	}
}

// Gets the required external tools for the Static Web App target
func (at *staticWebAppTarget) RequiredExternalTools(context.Context) []tools.ExternalTool {
	return []tools.ExternalTool{at.swa}
}

// Initializes the static web app target
func (at *staticWebAppTarget) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

// Sets the build output that will be consumed for the deploy operation
func (at *staticWebAppTarget) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServicePackageResult, ServiceProgress]) {
			if usingSwaConfig(packageOutput) {
				// The swa framework service does not set a packageOutput.PackagePath during package b/c the output
				// is governed by the swa-cli.config.json file.
				task.SetResult(packageOutput)
				return
			}

			// packageOutput.PackagePath != "" -> This means swa framework service is not used to build/deploy and there
			// is no a swa-cli.config.json file to govern the output.
			// In this case, the serviceConfig.OutputPath (azure.yaml -> service -> dist) defines where is the output from
			// the language framework service.
			// If serviceConfig.OutputPath is not defined, azd should use the package path defined by the language framework
			// service.

			packagePath := serviceConfig.OutputPath
			if strings.TrimSpace(packagePath) == "" {
				packagePath = packageOutput.PackagePath
			}

			task.SetResult(&ServicePackageResult{
				Build:       packageOutput.Build,
				PackagePath: packagePath,
			})
		},
	)
}

func usingSwaConfig(packageResult *ServicePackageResult) bool {
	// The swa framework service does not set a packageOutput.PackagePath during package b/c the output
	// is governed by the swa-cli.config.json file.
	return packageResult.PackagePath == ""
}

// Deploys the packaged build output using the SWA CLI
func (at *staticWebAppTarget) Deploy(
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

			// Get the static webapp deployment token
			task.SetProgress(NewServiceProgress("Retrieving deployment token"))
			deploymentToken, err := at.cli.GetStaticWebAppApiKey(
				ctx,
				targetResource.SubscriptionId(),
				targetResource.ResourceGroupName(),
				targetResource.ResourceName(),
			)
			if err != nil {
				task.SetError(fmt.Errorf("failed retrieving static web app deployment token: %w", err))
				return
			}

			// SWA performs a zip & deploy of the specified output folder and deploys it to the configured environment
			task.SetProgress(NewServiceProgress("swa cli deploy"))
			dOptions := swa.DeployOptions{}
			cwd := serviceConfig.Path()
			if !usingSwaConfig(packageOutput) {
				dOptions.AppFolderPath = serviceConfig.RelativePath
				dOptions.OutputRelativeFolderPath = packageOutput.PackagePath
				cwd = serviceConfig.Project.Path
			}
			res, err := at.swa.Deploy(ctx,
				cwd,
				at.env.GetTenantId(),
				targetResource.SubscriptionId(),
				targetResource.ResourceGroupName(),
				targetResource.ResourceName(),
				DefaultStaticWebAppEnvironmentName,
				*deploymentToken,
				dOptions)

			log.Println(res)

			if err != nil {
				task.SetError(fmt.Errorf("failed deploying static web app: %w", err))
				return
			}

			task.SetProgress(NewServiceProgress("Verifying deployment"))
			if err := at.verifyDeployment(ctx, targetResource); err != nil {
				task.SetError(err)
				return
			}

			task.SetProgress(NewServiceProgress("Fetching endpoints for static web app"))
			endpoints, err := at.Endpoints(ctx, serviceConfig, targetResource)
			if err != nil {
				task.SetError(err)
				return
			}

			sdr := NewServiceDeployResult(
				azure.StaticWebAppRID(
					targetResource.SubscriptionId(),
					targetResource.ResourceGroupName(),
					targetResource.ResourceName(),
				),
				StaticWebAppTarget,
				res,
				endpoints,
			)
			sdr.Package = packageOutput

			task.SetResult(sdr)
		},
	)
}

// Gets the endpoints for the static web app
func (at *staticWebAppTarget) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	// TODO: Enhance for multi-environment support
	// https://github.com/Azure/azure-dev/issues/1152
	if envProps, err := at.cli.GetStaticWebAppEnvironmentProperties(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
		DefaultStaticWebAppEnvironmentName,
	); err != nil {
		return nil, fmt.Errorf("fetching service properties: %w", err)
	} else {
		return []string{fmt.Sprintf("https://%s/", envProps.Hostname)}, nil
	}
}

func (at *staticWebAppTarget) validateTargetResource(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) error {
	if !strings.EqualFold(targetResource.ResourceType(), string(infra.AzureResourceTypeStaticWebSite)) {
		return resourceTypeMismatchError(
			targetResource.ResourceName(),
			targetResource.ResourceType(),
			infra.AzureResourceTypeStaticWebSite,
		)
	}

	return nil
}

func (at *staticWebAppTarget) verifyDeployment(ctx context.Context, targetResource *environment.TargetResource) error {
	retries := 0
	const maxRetries = 10

	for {
		envProps, err := at.cli.GetStaticWebAppEnvironmentProperties(
			ctx,
			targetResource.SubscriptionId(),
			targetResource.ResourceGroupName(),
			targetResource.ResourceName(),
			DefaultStaticWebAppEnvironmentName,
		)
		if err != nil {
			return fmt.Errorf("failed verifying static web app deployment: %w", err)
		}

		if envProps.Status == "Ready" {
			break
		}

		retries++

		if retries >= maxRetries {
			return fmt.Errorf("failed verifying static web app deployment. Still in %s state", envProps.Status)
		}

		time.Sleep(5 * time.Second)
	}

	return nil
}
