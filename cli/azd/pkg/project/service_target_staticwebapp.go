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
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/swa"
)

// TODO: Enhance for multi-environment support
// https://github.com/Azure/azure-dev/issues/1152
const DefaultStaticWebAppEnvironmentName = "default"

type staticWebAppTarget struct {
	env *environment.Environment
	cli *azapi.AzureClient
	swa *swa.Cli
}

// NewStaticWebAppTarget creates a new instance of the Static Web App target
func NewStaticWebAppTarget(
	env *environment.Environment,
	azCli *azapi.AzureClient,
	swaCli *swa.Cli,
) ServiceTarget {
	return &staticWebAppTarget{
		env: env,
		cli: azCli,
		swa: swaCli,
	}
}

// Gets the required external tools for the Static Web App target
func (at *staticWebAppTarget) RequiredExternalTools(ctx context.Context, serviceConfig *ServiceConfig) []tools.ExternalTool {
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
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	if usingSwaConfig(packageOutput) {
		// The swa framework service does not set a packageOutput.PackagePath during package b/c the output
		// is governed by the swa-cli.config.json file.
		return packageOutput, nil
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

	return &ServicePackageResult{
		Build:       packageOutput.Build,
		PackagePath: packagePath,
	}, nil
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
	progress *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	if err := at.validateTargetResource(targetResource); err != nil {
		return nil, fmt.Errorf("validating target resource: %w", err)
	}

	// Get the static webapp deployment token
	progress.SetProgress(NewServiceProgress("Retrieving deployment token"))
	deploymentToken, err := at.cli.GetStaticWebAppApiKey(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving static web app deployment token: %w", err)
	}

	// SWA performs a zip & deploy of the specified output folder and deploys it to the configured environment
	progress.SetProgress(NewServiceProgress("swa cli deploy"))
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
		return nil, fmt.Errorf("failed deploying static web app: %w", err)
	}

	progress.SetProgress(NewServiceProgress("Verifying deployment"))
	if err := at.verifyDeployment(ctx, targetResource); err != nil {
		return nil, err
	}

	progress.SetProgress(NewServiceProgress("Fetching endpoints for static web app"))
	endpoints, err := at.Endpoints(ctx, serviceConfig, targetResource)
	if err != nil {
		return nil, err
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

	return sdr, nil
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
	targetResource *environment.TargetResource,
) error {
	if !strings.EqualFold(targetResource.ResourceType(), string(azapi.AzureResourceTypeStaticWebSite)) {
		return resourceTypeMismatchError(
			targetResource.ResourceName(),
			targetResource.ResourceType(),
			azapi.AzureResourceTypeStaticWebSite,
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
