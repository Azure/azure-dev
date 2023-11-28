package project

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type registryTarget struct {
	env             *environment.Environment
	containerHelper *ContainerHelper
}

func NewRegistryTarget(
	env *environment.Environment,
	containerHelper *ContainerHelper,
) ServiceTarget {
	return &registryTarget{
		env:             env,
		containerHelper: containerHelper,
	}
}

func (r *registryTarget) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

func (r *registryTarget) RequiredExternalTools(ctx context.Context) []tools.ExternalTool {
	return r.containerHelper.RequiredExternalTools(ctx)
}

func (r *registryTarget) Package(
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

func (r *registryTarget) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
	targetResource *environment.TargetResource,
) *async.TaskWithProgress[*ServiceDeployResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServiceDeployResult, ServiceProgress]) {
			if err := r.validateTargetResource(ctx, serviceConfig, targetResource); err != nil {
				task.SetError(fmt.Errorf("validating target resource: %w", err))
				return
			}

			// Login, tag & push container image to ACR
			containerDeployTask := r.containerHelper.Deploy(ctx, serviceConfig, packageOutput, targetResource)
			syncProgress(task, containerDeployTask.Progress())

			_, err := containerDeployTask.Await()
			if err != nil {
				task.SetError(err)
				return
			}

			task.SetProgress(NewServiceProgress("Fetching endpoints for container registry service"))
			endpoints, err := r.Endpoints(ctx, serviceConfig, targetResource)
			if err != nil {
				task.SetError(err)
				return
			}

			task.SetResult(&ServiceDeployResult{
				Package: packageOutput,
				TargetResourceId: azure.ContainerAppRID(
					targetResource.SubscriptionId(),
					targetResource.ResourceGroupName(),
					targetResource.ResourceName(),
				),
				Kind:      RegistryTarget,
				Endpoints: endpoints,
			})
		},
	)
}

func (r *registryTarget) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	imageName := r.env.GetServiceProperty(serviceConfig.Name, "IMAGE_NAME")
	return []string{imageName}, nil
}

func (r *registryTarget) validateTargetResource(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) error {
	if targetResource.ResourceGroupName() == "" {
		return fmt.Errorf("missing resource group name: %s", targetResource.ResourceGroupName())
	}

	if targetResource.ResourceType() != "" {
		if err := checkResourceType(targetResource, infra.AzureResourceTypeContainerRegistry); err != nil {
			return err
		}
	}

	return nil
}
