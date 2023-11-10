// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/containerapps"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
)

type dotnetContainerAppTarget struct {
	env                 *environment.Environment
	containerHelper     *ContainerHelper
	containerAppService containerapps.ContainerAppService
	resourceManager     ResourceManager
	dotNetCli           dotnet.DotNetCli
}

// NewDotNetContainerAppTarget creates the Service Target for a Container App that is written in .NET. Unlike
// [ContainerAppTarget], this target does not require a Dockerfile to be present in the project. Instead, it uses the built
// in support in .NET 8 for publishing containers using `dotnet publish`. In addition, it uses a different deployment
// strategy built on a yaml manifest file, using the same format `az containerapp create --yaml`, with additional support
// for using text/template to do replacements, similar to tools like Helm.
//
// Note that unlike [ContainerAppTarget] this target does not add SERVICE_<XYZ>_IMAGE_NAME values to the environment,
// instead, the image name is present on the context object used when rendering the template.
func NewDotNetContainerAppTarget(
	env *environment.Environment,
	containerHelper *ContainerHelper,
	containerAppService containerapps.ContainerAppService,
	resourceManager ResourceManager,
	dotNetCli dotnet.DotNetCli,
) ServiceTarget {
	return &dotnetContainerAppTarget{
		env:                 env,
		containerHelper:     containerHelper,
		containerAppService: containerAppService,
		resourceManager:     resourceManager,
		dotNetCli:           dotNetCli,
	}
}

// Gets the required external tools
func (at *dotnetContainerAppTarget) RequiredExternalTools(ctx context.Context) []tools.ExternalTool {
	return tools.Unique(append(at.containerHelper.RequiredExternalTools(ctx), at.dotNetCli))
}

// Initializes the Container App target
func (at *dotnetContainerAppTarget) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

// Prepares and tags the container image from the build output based on the specified service configuration
func (at *dotnetContainerAppTarget) Package(
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

// Deploys service container images to ACR and provisions the container app service.
func (at *dotnetContainerAppTarget) Deploy(
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

			task.SetProgress(NewServiceProgress("Logging in to registry"))

			// Login, tag & push container image to ACR
			loginServer, err := at.containerHelper.Login(ctx, targetResource)
			if err != nil {
				task.SetError(fmt.Errorf("logging in to registry: %w", err))
				return
			}

			task.SetProgress(NewServiceProgress("Pushing container image"))

			imageName := fmt.Sprintf("azd-deploy-%s-%d", serviceConfig.Name, time.Now().Unix())

			err = at.dotNetCli.PublishContainer(ctx, serviceConfig.Path(), "Debug", imageName, loginServer)
			if err != nil {
				task.SetError(fmt.Errorf("publishing container: %w", err))
				return
			}

			task.SetProgress(NewServiceProgress("Updating container app"))

			projectRoot := serviceConfig.Path()
			if f, err := os.Stat(projectRoot); err == nil && !f.IsDir() {
				projectRoot = filepath.Dir(projectRoot)
			}

			manifest, err := os.ReadFile(filepath.Join(projectRoot, "manifests", "containerApp.tmpl.yaml"))
			if err != nil {
				task.SetError(fmt.Errorf("reading container app manifest: %w", err))
				return
			}

			tmpl, err := template.New("containerApp.tmpl.yaml").
				Option("missingkey=error").
				Parse(string(manifest))
			if err != nil {
				task.SetError(fmt.Errorf("failing parsing containerApp.tmpl.yaml: %w", err))
				return
			}

			builder := strings.Builder{}
			err = tmpl.Execute(&builder, struct {
				Env   map[string]string
				Image string
			}{
				Env:   at.env.Dotenv(),
				Image: fmt.Sprintf("%s/%s", loginServer, imageName),
			})
			if err != nil {
				task.SetError(fmt.Errorf("failed executing template file: %w", err))
				return
			}

			err = at.containerAppService.DeployYaml(
				ctx,
				targetResource.SubscriptionId(),
				targetResource.ResourceGroupName(),
				serviceConfig.Name,
				[]byte(builder.String()),
			)
			if err != nil {
				task.SetError(fmt.Errorf("updating container app service: %w", err))
				return
			}

			task.SetProgress(NewServiceProgress("Fetching endpoints for container app service"))

			containerAppTarget := environment.NewTargetResource(
				targetResource.SubscriptionId(),
				targetResource.ResourceGroupName(),
				serviceConfig.Name,
				string(infra.AzureResourceTypeContainerApp))

			endpoints, err := at.Endpoints(ctx, serviceConfig, containerAppTarget)
			if err != nil {
				task.SetError(err)
				return
			}

			task.SetResult(&ServiceDeployResult{
				Package: packageOutput,
				TargetResourceId: azure.ContainerAppRID(
					targetResource.SubscriptionId(),
					targetResource.ResourceGroupName(),
					serviceConfig.Name,
				),
				Kind:      ContainerAppTarget,
				Endpoints: endpoints,
			})
		},
	)
}

// Gets endpoint for the container app service
func (at *dotnetContainerAppTarget) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	if ingressConfig, err := at.containerAppService.GetIngressConfiguration(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
	); err != nil {
		return nil, fmt.Errorf("fetching service properties: %w", err)
	} else {
		endpoints := make([]string, len(ingressConfig.HostNames))
		for idx, hostName := range ingressConfig.HostNames {
			endpoints[idx] = fmt.Sprintf("https://%s/", hostName)
		}

		return endpoints, nil
	}
}

func (at *dotnetContainerAppTarget) validateTargetResource(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) error {
	if targetResource.ResourceGroupName() == "" {
		return fmt.Errorf("missing resource group name: %s", targetResource.ResourceGroupName())
	}

	if targetResource.ResourceType() != "" {
		if err := checkResourceType(targetResource, infra.AzureResourceTypeContainerAppEnvironment); err != nil {
			return err
		}
	}

	return nil
}
