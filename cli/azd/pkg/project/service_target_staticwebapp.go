// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
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
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	// Extract package result from service context
	var packagePath string
	var hasSwaConfig bool

	// Check for SWA config
	if _, found := serviceContext.Package.FindFirst(WithKind(ArtifactKindConfig)); found {
		hasSwaConfig = true
	}

	// Get build output path
	if artifact, found := serviceContext.Package.FindFirst(WithKind(ArtifactKindDirectory)); found {
		packagePath = artifact.Location
	}

	if hasSwaConfig {
		// The swa framework service does not set a packagePath during package b/c the output
		// is governed by the swa-cli.config.json file.
		return &ServicePackageResult{
			Artifacts: ArtifactCollection{
				{
					Kind:         ArtifactKindConfig,
					Location:     "saw-cli.config.json",
					LocationKind: LocationKindLocal,
				},
			},
		}, nil
	}

	// packagePath != "" -> This means swa framework service is not used to build/deploy and there
	// is no a swa-cli.config.json file to govern the output.
	// In this case, the serviceConfig.OutputPath (azure.yaml -> service -> dist) defines where is the output from
	// the language framework service.
	// If serviceConfig.OutputPath is not defined, azd should use the package path defined by the language framework
	// service.

	if strings.TrimSpace(serviceConfig.OutputPath) != "" {
		packagePath = serviceConfig.OutputPath
	}

	return &ServicePackageResult{
		Artifacts: ArtifactCollection{
			{
				Kind:         ArtifactKindDirectory,
				Location:     packagePath,
				LocationKind: LocationKindLocal,
			},
		},
	}, nil
}

func usingSwaConfig(artifacts ArtifactCollection) bool {
	// Check if swa-config artifact exists in the package result
	_, found := artifacts.FindFirst(WithKind(ArtifactKindConfig))
	return found
}

func (at *staticWebAppTarget) Publish(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
	publishOptions *PublishOptions,
) (*ServicePublishResult, error) {
	return &ServicePublishResult{}, nil
}

// Deploys the packaged build output using the SWA CLI
func (at *staticWebAppTarget) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
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
	if !usingSwaConfig(serviceContext.Package) {
		// Extract package path from artifacts
		var packagePath string
		if artifact, found := serviceContext.Package.FindFirst(WithKind(ArtifactKindDirectory)); found {
			packagePath = artifact.Location
		}

		if (packagePath == "" || cwd == packagePath) &&
			cwd == serviceConfig.Project.Path {
			return nil, &internal.ErrorWithSuggestion{
				Err: fmt.Errorf("service source and output folder cannot be at the root: %s", serviceConfig.RelativePath),
				Suggestion: strings.Join([]string{
					"If your service is at the root of your project, next to azure.yaml, move your service to a subfolder.",
					"Azure Static Web Apps does not support deploying from a folder that is for both the service" +
						" source and the output folder.",
					"Update the path of the service in azure.yaml to point to the subfolder and try deploying again.",
				}, "\n"),
			}
		}

		dOptions.AppFolderPath = serviceConfig.RelativePath
		dOptions.OutputRelativeFolderPath = packagePath
		cwd = serviceConfig.Project.Path
	}
	_, err = at.swa.Deploy(ctx,
		cwd,
		at.env.GetTenantId(),
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
		DefaultStaticWebAppEnvironmentName,
		*deploymentToken,
		dOptions)

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

	artifacts := ArtifactCollection{}

	// Add endpoints as artifacts
	for _, endpoint := range endpoints {
		if err := artifacts.Add(&Artifact{
			Kind:         ArtifactKindEndpoint,
			Location:     endpoint,
			LocationKind: LocationKindRemote,
		}); err != nil {
			return nil, fmt.Errorf("failed to add endpoint artifact: %w", err)
		}
	}

	// Add resource artifact
	resourceArtifact := Artifact{}
	if err := mapper.Convert(targetResource, &resourceArtifact); err == nil {
		if err := artifacts.Add(&resourceArtifact); err != nil {
			return nil, fmt.Errorf("failed to add resource artifact: %w", err)
		}
	}

	return &ServiceDeployResult{
		Artifacts: artifacts,
	}, nil
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
