// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

const (
	defaultDeploymentName = "default"
)

// The Azure Spring Apps configuration options
type SpringOptions struct {
	// The deployment name of ASA app
	DeploymentName string `yaml:"deploymentName"`
}

type springAppTarget struct {
	env           *environment.Environment
	envManager    environment.Manager
	springService azapi.SpringService
}

// NewSpringAppTarget creates the spring app service target.
//
// The target resource can be partially filled with only ResourceGroupName, since spring apps
// can be provisioned during deployment.
func NewSpringAppTarget(
	env *environment.Environment,
	envManager environment.Manager,
	springService azapi.SpringService,
) ServiceTarget {
	return &springAppTarget{
		env:           env,
		envManager:    envManager,
		springService: springService,
	}
}

func (st *springAppTarget) RequiredExternalTools(ctx context.Context, serviceConfig *ServiceConfig) []tools.ExternalTool {
	return []tools.ExternalTool{}
}

func (st *springAppTarget) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

// Do nothing for Spring Apps
func (st *springAppTarget) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	return &ServicePackageResult{}, nil
}

func (st *springAppTarget) Publish(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
	publishOptions *PublishOptions,
) (*ServicePublishResult, error) {
	return &ServicePublishResult{}, nil
}

// Upload artifact to Storage File and deploy to Spring App
func (st *springAppTarget) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	if err := st.validateTargetResource(targetResource); err != nil {
		return nil, fmt.Errorf("validating target resource: %w", err)
	}

	deploymentName := serviceConfig.Spring.DeploymentName
	if deploymentName == "" {
		deploymentName = defaultDeploymentName
	}

	_, err := st.springService.GetSpringAppDeployment(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
		serviceConfig.Name,
		deploymentName,
	)

	if err != nil {
		return nil, fmt.Errorf("get deployment '%s' of Spring App '%s' failed: %w", serviceConfig.Name, deploymentName, err)
	}

	// Extract package path from service context artifacts
	var packagePath string
	if artifact, found := serviceContext.Package.FindFirst(WithKind(ArtifactKindDirectory)); found {
		packagePath = artifact.Location
	}
	if packagePath == "" {
		return nil, fmt.Errorf("no package artifact found in service context")
	}

	// TODO: Consider support container image and buildpacks deployment in the future
	// For now, Azure Spring Apps only support jar deployment
	ext := ".jar"
	artifactPath := filepath.Join(packagePath, AppServiceJavaPackageName+ext)

	_, err = os.Stat(artifactPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("artifact %s does not exist: %w", artifactPath, err)
	}
	if err != nil {
		return nil, fmt.Errorf("reading artifact file %s: %w", artifactPath, err)
	}

	progress.SetProgress(NewServiceProgress("Uploading spring artifact"))

	relativePath, err := st.springService.UploadSpringArtifact(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
		serviceConfig.Name,
		artifactPath,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to upload spring artifact: %w", err)
	}

	progress.SetProgress(NewServiceProgress("Deploying spring artifact"))

	deployResult, err := st.springService.DeploySpringAppArtifact(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
		serviceConfig.Name,
		*relativePath,
		deploymentName,
	)
	if err != nil {
		return nil, fmt.Errorf("deploying service %s: %w", serviceConfig.Name, err)
	}

	// save the storage relative, otherwise the relative path will be overwritten
	// in the deployment from Bicep/Terraform
	st.env.SetServiceProperty(serviceConfig.Name, "RELATIVE_PATH", *relativePath)
	if err := st.envManager.Save(ctx, st.env); err != nil {
		return nil, fmt.Errorf("failed updating environment with relative path, %w", err)
	}

	progress.SetProgress(NewServiceProgress("Fetching endpoints for spring app service"))
	endpoints, err := st.Endpoints(ctx, serviceConfig, targetResource)
	if err != nil {
		return nil, err
	}

	artifacts := ArtifactCollection{}

	// Add deployment result as artifact
	if deployResult != nil {
		if err := artifacts.Add(&Artifact{
			Kind:         ArtifactKindDeployment,
			Location:     *deployResult,
			LocationKind: LocationKindRemote,
			Metadata: map[string]string{
				"deploymentName": deploymentName,
				"serviceName":    serviceConfig.Name,
				"resourceName":   targetResource.ResourceName(),
				"resourceType":   targetResource.ResourceType(),
				"subscription":   targetResource.SubscriptionId(),
				"resourceGroup":  targetResource.ResourceGroupName(),
				"relativePath":   *relativePath,
			},
		}); err != nil {
			return nil, fmt.Errorf("failed to add deployment artifact: %w", err)
		}
	}

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

// Gets the exposed endpoints for the Spring Apps Service
func (st *springAppTarget) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	springAppProperties, err := st.springService.GetSpringAppProperties(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
		serviceConfig.Name,
	)
	if err != nil {
		return nil, fmt.Errorf("fetching service properties: %w", err)
	}

	return springAppProperties.Url, nil
}

func (st *springAppTarget) validateTargetResource(
	targetResource *environment.TargetResource,
) error {
	if targetResource.ResourceGroupName() == "" {
		return fmt.Errorf("missing resource group name: %s", targetResource.ResourceGroupName())
	}

	if targetResource.ResourceType() != "" {
		if err := checkResourceType(targetResource, azapi.AzureResourceTypeSpringApp); err != nil {
			return err
		}
	}

	return nil
}
