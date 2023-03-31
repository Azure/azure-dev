// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

type springAppTarget struct {
	env *environment.Environment
	cli azcli.AzCli
}

// NewSpringAppTarget creates the spring app service target.
//
// The target resource can be partially filled with only ResourceGroupName, since spring apps
// can be provisioned during deployment.
func NewSpringAppTarget(
	env *environment.Environment,
	azCli azcli.AzCli,
) ServiceTarget {
	return &springAppTarget{
		env: env,
		cli: azCli,
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

			_, err := st.cli.GetSpringAppDeployment(
				ctx,
				targetResource.SubscriptionId(),
				targetResource.ResourceGroupName(),
				targetResource.ResourceName(),
				serviceConfig.Name,
				serviceConfig.DeploymentName,
			)

			if err != nil {
				task.SetError(fmt.Errorf("Spring Apps '%s' deployment '%s' not exists", serviceConfig.Name, serviceConfig.DeploymentName))
				return
			}

			artifactPath := filepath.Join(packageOutput.PackagePath, AppServiceJavaPackageName)

			if _, err := os.Stat(artifactPath); err != nil {
				task.SetError(fmt.Errorf("artifact don't exists: %w", err))
				return
			}

			task.SetProgress(NewServiceProgress("Uploading spring artifact"))

			relativePath, err := st.cli.UploadSpringArtifact(
				ctx,
				targetResource.SubscriptionId(),
				targetResource.ResourceGroupName(),
				targetResource.ResourceName(),
				serviceConfig.Name,
				artifactPath,
			)

			if err != nil {
				task.SetError(fmt.Errorf("Artifact upload failed: %w", err))
				return
			}

			task.SetProgress(NewServiceProgress("Deploying spring artifact"))

			res, err := st.cli.DeploySpringAppArtifact(
				ctx,
				targetResource.SubscriptionId(),
				targetResource.ResourceGroupName(),
				targetResource.ResourceName(),
				serviceConfig.Name,
				*relativePath,
				serviceConfig.DeploymentName,
			)
			if err != nil {
				task.SetError(fmt.Errorf("deploying service %s: %w", serviceConfig.Name, err))
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
	appServiceProperties, err := st.cli.GetSpringAppProperties(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
		serviceConfig.Name,
	)
	if err != nil {
		return nil, fmt.Errorf("fetching service properties: %w", err)
	}

	endpoints := make([]string, len(appServiceProperties.Fqdn))
	for idx, fqdn := range appServiceProperties.Fqdn {
		endpoints[idx] = fmt.Sprintf("https://%s/", st.extractEndpoint(fqdn, serviceConfig.Name))
	}

	return endpoints, nil
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

func (st *springAppTarget) extractEndpoint(fqdn string, appName string) string {
	index := strings.IndexRune(fqdn, '.')
	return fqdn[0:index] + "-" + appName + fqdn[index:]
}
