package project

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/operations"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/benbjohnson/clock"
)

type ContainerHelper struct {
	env                      *environment.Environment
	containerRegistryService azcli.ContainerRegistryService
	docker                   docker.Docker
	clock                    clock.Clock
	operationManager         operations.Manager
}

func NewContainerHelper(
	env *environment.Environment,
	clock clock.Clock,
	containerRegistryService azcli.ContainerRegistryService,
	docker docker.Docker,
	operationManager operations.Manager,
) *ContainerHelper {
	return &ContainerHelper{
		env:                      env,
		containerRegistryService: containerRegistryService,
		docker:                   docker,
		clock:                    clock,
		operationManager:         operationManager,
	}
}

func (ch *ContainerHelper) RegistryName(ctx context.Context) (string, error) {
	loginServer, has := ch.env.LookupEnv(environment.ContainerRegistryEndpointEnvVarName)
	if !has {
		return "", fmt.Errorf(
			"could not determine container registry endpoint, ensure %s is set as an output of your infrastructure",
			environment.ContainerRegistryEndpointEnvVarName,
		)
	}

	return loginServer, nil
}

func (ch *ContainerHelper) RemoteImageTag(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	localImageTag string,
) (string, error) {
	loginServer, err := ch.RegistryName(ctx)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(
		"%s/%s",
		loginServer,
		localImageTag,
	), nil
}

func (ch *ContainerHelper) LocalImageTag(ctx context.Context, serviceConfig *ServiceConfig) (string, error) {
	configuredTag, err := serviceConfig.Docker.Tag.Envsubst(ch.env.Getenv)
	if err != nil {
		return "", err
	}

	if configuredTag != "" {
		return configuredTag, nil
	}

	return fmt.Sprintf("%s/%s-%s:azd-deploy-%d",
		strings.ToLower(serviceConfig.Project.Name),
		strings.ToLower(serviceConfig.Name),
		strings.ToLower(ch.env.GetEnvName()),
		ch.clock.Now().Unix(),
	), nil
}

func (ch *ContainerHelper) RequiredExternalTools(context.Context) []tools.ExternalTool {
	return []tools.ExternalTool{ch.docker}
}

func (ch *ContainerHelper) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
	targetResource *environment.TargetResource,
) *async.TaskWithProgress[*ServiceDeployResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServiceDeployResult, ServiceProgress]) {
			// Get ACR Login Server
			loginServer, err := ch.RegistryName(ctx)
			if err != nil {
				task.SetError(err)
				return
			}

			localImageTag := packageOutput.PackagePath
			packageDetails, ok := packageOutput.Details.(*dockerPackageResult)
			if ok && packageDetails != nil {
				localImageTag = packageDetails.ImageTag
			}

			if localImageTag == "" {
				task.SetError(errors.New("failed retrieving package result details"))
				return
			}

			// Tag image
			// Get remote tag from the container helper then call docker cli tag command
			remoteTag, err := ch.RemoteImageTag(ctx, serviceConfig, localImageTag)
			if err != nil {
				task.SetError(fmt.Errorf("getting remote image tag: %w", err))
				return
			}

			ch.operationManager.ReportProgress(ctx, "Tagging container image")
			if err := ch.docker.Tag(ctx, serviceConfig.Path(), localImageTag, remoteTag); err != nil {
				task.SetError(err)
				return
			}

			log.Printf("logging into container registry '%s'\n", loginServer)
			ch.operationManager.ReportProgress(ctx, "Logging into container registry")
			err = ch.containerRegistryService.Login(ctx, targetResource.SubscriptionId(), loginServer)
			if err != nil {
				task.SetError(err)
				return
			}

			// Push image.
			log.Printf("pushing %s to registry", remoteTag)
			ch.operationManager.ReportProgress(ctx, "Pushing container image")
			if err := ch.docker.Push(ctx, serviceConfig.Path(), remoteTag); err != nil {
				task.SetError(err)
				return
			}

			// Save the name of the image we pushed into the environment with a well known key.
			log.Printf("writing image name to environment")
			ch.env.SetServiceProperty(serviceConfig.Name, "IMAGE_NAME", remoteTag)

			if err := ch.env.Save(); err != nil {
				task.SetError(fmt.Errorf("saving image name to environment: %w", err))
				return
			}

			task.SetResult(&ServiceDeployResult{
				Package: packageOutput,
			})
		})
}
