// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

// functionAppTarget specifies an Azure Function to deploy to.
// Implements `project.ServiceTarget`
type functionAppTarget struct {
	env *environment.Environment
	cli *azapi.AzureClient
}

// NewFunctionAppTarget creates a new instance of the Function App target
func NewFunctionAppTarget(
	env *environment.Environment,
	azCli *azapi.AzureClient,
) ServiceTarget {
	return &functionAppTarget{
		env: env,
		cli: azCli,
	}
}

// Gets the required external tools for the Function app
func (f *functionAppTarget) RequiredExternalTools(ctx context.Context, serviceConfig *ServiceConfig) []tools.ExternalTool {
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
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	// Extract build artifact from service context
	var buildPath string
	if artifact, found := serviceContext.Package.FindFirst(WithKind(ArtifactKindDirectory)); found {
		buildPath = artifact.Location
	}
	if buildPath == "" {
		return nil, fmt.Errorf("no build result found in service context")
	}

	var err error
	zipFilePath := buildPath
	if filepath.Ext(buildPath) != ".zip" {
		progress.SetProgress(NewServiceProgress("Compressing deployment artifacts"))
		zipFilePath, err = createDeployableZip(
			serviceConfig,
			buildPath,
		)
		if err != nil {
			return nil, err
		}
	}

	return &ServicePackageResult{
		Artifacts: ArtifactCollection{
			{
				Kind:         ArtifactKindArchive,
				Location:     zipFilePath,
				LocationKind: LocationKindLocal,
			},
		},
	}, nil
}

func (f *functionAppTarget) Publish(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
	publishOptions *PublishOptions,
) (*ServicePublishResult, error) {
	return &ServicePublishResult{}, nil
}

// Deploys the prepared zip archive using Zip deploy to the Azure App Service resource
func (f *functionAppTarget) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	if err := f.validateTargetResource(targetResource); err != nil {
		return nil, fmt.Errorf("validating target resource: %w", err)
	}

	// Extract zip package from service context
	var zipFilePath string
	if artifact, found := serviceContext.Package.FindFirst(WithKind(ArtifactKindArchive)); found {
		zipFilePath = artifact.Location
	}
	if zipFilePath == "" {
		return nil, fmt.Errorf("no zip package found in service context")
	}

	zipFile, err := os.Open(zipFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed reading deployment zip file: %w", err)
	}

	defer zipFile.Close()

	progress.SetProgress(NewServiceProgress("Uploading deployment package"))
	remoteBuild := serviceConfig.Language == ServiceLanguageJavaScript ||
		serviceConfig.Language == ServiceLanguageTypeScript ||
		serviceConfig.Language == ServiceLanguagePython

	_, err = f.cli.DeployFunctionAppUsingZipFile(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
		zipFile,
		remoteBuild,
	)
	if err != nil {
		return nil, err
	}

	progress.SetProgress(NewServiceProgress("Fetching endpoints for function app"))
	endpoints, err := f.Endpoints(ctx, serviceConfig, targetResource)
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
	var resourceArtifact *Artifact
	if err := mapper.Convert(targetResource, &resourceArtifact); err == nil {
		if err := artifacts.Add(resourceArtifact); err != nil {
			return nil, fmt.Errorf("failed to add resource artifact: %w", err)
		}
	}

	return &ServiceDeployResult{
		Artifacts: artifacts,
	}, nil
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
	targetResource *environment.TargetResource,
) error {
	if !strings.EqualFold(targetResource.ResourceType(), string(azapi.AzureResourceTypeWebSite)) {
		return resourceTypeMismatchError(
			targetResource.ResourceName(),
			targetResource.ResourceType(),
			azapi.AzureResourceTypeWebSite,
		)
	}

	return nil
}
