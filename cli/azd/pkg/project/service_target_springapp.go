// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
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
	springService azcli.SpringService
}

// NewSpringAppTarget creates the spring app service target.
//
// The target resource can be partially filled with only ResourceGroupName, since spring apps
// can be provisioned during deployment.
func NewSpringAppTarget(
	env *environment.Environment,
	envManager environment.Manager,
	springService azcli.SpringService,
) ServiceTarget {
	return &springAppTarget{
		env:           env,
		envManager:    envManager,
		springService: springService,
	}
}

func (st *springAppTarget) RequiredExternalTools(context.Context) []tools.ExternalTool {
	return []tools.ExternalTool{}
}

func (st *springAppTarget) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

// Do nothing for Spring Apps
func (st *springAppTarget) Package(
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

// Upload artifact to Storage File and deploy to Spring App
func (st *springAppTarget) Deploy(
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
				task.SetError(fmt.Errorf("get deployment '%s' of Spring App '%s' failed: %w",
					serviceConfig.Name, deploymentName, err))
				return
			}

			// TODO: Consider support container image and buildpacks deployment in the future
			// For now, Azure Spring Apps only support jar deployment
			ext := ".jar"
			artifactPath := filepath.Join(packageOutput.PackagePath, AppServiceJavaPackageName+ext)

			_, err = os.Stat(artifactPath)
			if errors.Is(err, os.ErrNotExist) {
				task.SetError(fmt.Errorf("artifact %s does not exist: %w", artifactPath, err))
				return
			}
			if err != nil {
				task.SetError(fmt.Errorf("reading artifact file %s: %w", artifactPath, err))
				return
			}

			task.SetProgress(NewServiceProgress("Uploading spring artifact"))

			relativePath, err := st.springService.UploadSpringArtifact(
				ctx,
				targetResource.SubscriptionId(),
				targetResource.ResourceGroupName(),
				targetResource.ResourceName(),
				serviceConfig.Name,
				artifactPath,
			)

			if err != nil {
				task.SetError(fmt.Errorf("failed to upload spring artifact: %w", err))
				return
			}

			task.SetProgress(NewServiceProgress("Deploying spring artifact"))

			res, err := st.springService.DeploySpringAppArtifact(
				ctx,
				targetResource.SubscriptionId(),
				targetResource.ResourceGroupName(),
				targetResource.ResourceName(),
				serviceConfig.Name,
				*relativePath,
				deploymentName,
			)
			if err != nil {
				task.SetError(fmt.Errorf("deploying service %s: %w", serviceConfig.Name, err))
				return
			}

			// save the storage relative, otherwise the relative path will be overwritten
			// in the deployment from Bicep/Terraform
			st.env.SetServiceProperty(serviceConfig.Name, "RELATIVE_PATH", *relativePath)
			if err := st.envManager.Save(ctx, st.env); err != nil {
				task.SetError(fmt.Errorf("failed updating environment with relative path, %w", err))
				return
			}

			task.SetProgress(NewServiceProgress("Fetching endpoints for spring app service"))
			endpoints, err := st.Endpoints(ctx, serviceConfig, targetResource)
			if err != nil {
				task.SetError(err)
				return
			}

			sdr := NewServiceDeployResult(
				azure.SpringAppRID(
					targetResource.SubscriptionId(),
					targetResource.ResourceGroupName(),
					targetResource.ResourceName(),
				),
				SpringAppTarget,
				*res,
				endpoints,
			)
			sdr.Package = packageOutput

			task.SetResult(sdr)

		},
	)
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
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) error {
	if targetResource.ResourceGroupName() == "" {
		return fmt.Errorf("missing resource group name: %s", targetResource.ResourceGroupName())
	}

	if targetResource.ResourceType() != "" {
		if err := checkResourceType(targetResource, infra.AzureResourceTypeSpringApp); err != nil {
			return err
		}
	}

	return nil
}
