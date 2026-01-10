// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type appServiceTarget struct {
	env *environment.Environment
	cli *azapi.AzureClient
}

// NewAppServiceTarget creates a new instance of the AppServiceTarget
func NewAppServiceTarget(
	env *environment.Environment,
	azCli *azapi.AzureClient,
) ServiceTarget {
	return &appServiceTarget{
		env: env,
		cli: azCli,
	}
}

// Gets the required external tools
func (st *appServiceTarget) RequiredExternalTools(ctx context.Context, serviceConfig *ServiceConfig) []tools.ExternalTool {
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
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	progress.SetProgress(NewServiceProgress("Compressing deployment artifacts"))

	// Get package path from the service context
	var packagePath string
	if artifact, found := serviceContext.Package.FindFirst(WithKind(ArtifactKindDirectory)); found &&
		artifact.Location != "" {
		packagePath = artifact.Location
	}

	if packagePath == "" {
		return nil, fmt.Errorf("no package artifacts found in service context")
	}

	zipFilePath, err := createDeployableZip(
		serviceConfig,
		packagePath,
	)
	if err != nil {
		return nil, err
	}

	// Create zip artifact
	zipArtifact := &Artifact{
		Kind:         ArtifactKindArchive,
		Location:     zipFilePath,
		LocationKind: LocationKindLocal,
		Metadata: map[string]string{
			"packagePath": packagePath,
		},
	}

	return &ServicePackageResult{
		Artifacts: ArtifactCollection{zipArtifact},
	}, nil
}

func (st *appServiceTarget) Publish(
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
func (st *appServiceTarget) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	if err := st.validateTargetResource(targetResource); err != nil {
		return nil, fmt.Errorf("validating target resource: %w", err)
	}

	// Get zip file path from package artifacts
	var zipFilePath string
	if artifact, found := serviceContext.Package.FindFirst(WithKind(ArtifactKindArchive)); found && artifact.Location != "" {
		zipFilePath = artifact.Location
	}

	if zipFilePath == "" {
		return nil, fmt.Errorf("no zip artifacts found in service context")
	}

	zipFile, err := os.Open(zipFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed reading deployment zip file: %w", err)
	}

	defer zipFile.Close()

	progress.SetProgress(NewServiceProgress("Uploading deployment package"))
	_, err = st.cli.DeployAppServiceZip(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
		zipFile,
		func(logProgress string) { progress.SetProgress(NewServiceProgress(logProgress)) },
	)
	if err != nil {
		return nil, fmt.Errorf("deploying service %s: %w", serviceConfig.Name, err)
	}

	progress.SetProgress(NewServiceProgress("Fetching endpoints for app service"))
	endpoints, err := st.Endpoints(ctx, serviceConfig, targetResource)
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
