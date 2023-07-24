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
	logProgress LogProgressFunc,
) (ServicePackageResult, error) {
	packagePath := serviceConfig.OutputPath
	if strings.TrimSpace(packagePath) == "" {
		packagePath = "build"
	}

	return ServicePackageResult{
		Build:       packageOutput.Build,
		PackagePath: packagePath,
	}, nil
}

// Deploys the packaged build output using the SWA CLI
func (at *staticWebAppTarget) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
	targetResource *environment.TargetResource,
	logProgress LogProgressFunc,
) (ServiceDeployResult, error) {
	if err := at.validateTargetResource(ctx, serviceConfig, targetResource); err != nil {
		return ServiceDeployResult{}, fmt.Errorf("validating target resource: %w", err)
	}

	// Get the static webapp deployment token
	logProgress("Retrieving deployment token")
	deploymentToken, err := at.cli.GetStaticWebAppApiKey(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
	)
	if err != nil {
		return ServiceDeployResult{}, fmt.Errorf("failed retrieving static web app deployment token: %w", err)
	}

	// SWA performs a zip & deploy of the specified output folder and deploys it to the configured environment
	logProgress("Uploading deployment artifacts")
	res, err := at.swa.Deploy(ctx,
		serviceConfig.Project.Path,
		at.env.GetTenantId(),
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
		serviceConfig.RelativePath,
		packageOutput.PackagePath,
		DefaultStaticWebAppEnvironmentName,
		*deploymentToken)

	log.Println(res)

	if err != nil {
		return ServiceDeployResult{}, fmt.Errorf("failed deploying static web app: %w", err)
	}

	logProgress("Verifying deployment")
	if err := at.verifyDeployment(ctx, targetResource); err != nil {
		return ServiceDeployResult{}, err
	}

	logProgress("Fetching endpoints for static web app")
	endpoints, err := at.Endpoints(ctx, serviceConfig, targetResource)
	if err != nil {
		return ServiceDeployResult{}, err
	}

	sdr := ServiceDeployResult{
		TargetResourceId: azure.StaticWebAppRID(
			targetResource.SubscriptionId(),
			targetResource.ResourceGroupName(),
			targetResource.ResourceName(),
		),
		Kind:      StaticWebAppTarget,
		Endpoints: endpoints,
		Details:   res,
		Package:   packageOutput,
	}
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
