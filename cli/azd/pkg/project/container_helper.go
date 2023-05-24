package project

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/messaging"
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
	publisher                messaging.Publisher
}

func NewContainerHelper(
	env *environment.Environment,
	clock clock.Clock,
	containerRegistryService azcli.ContainerRegistryService,
	docker docker.Docker,
	publisher messaging.Publisher,
) *ContainerHelper {
	return &ContainerHelper{
		env:                      env,
		containerRegistryService: containerRegistryService,
		docker:                   docker,
		clock:                    clock,
		publisher:                publisher,
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
) (*ServiceDeployResult, error) {
	// Get ACR Login Server
	loginServer, err := ch.RegistryName(ctx)
	if err != nil {
		return nil, err
	}

	localImageTag := packageOutput.PackagePath
	packageDetails, ok := packageOutput.Details.(*dockerPackageResult)
	if ok && packageDetails != nil {
		localImageTag = packageDetails.ImageTag
	}

	if localImageTag == "" {
		return nil, errors.New("failed retrieving package result details")
	}

	// Tag image
	// Get remote tag from the container helper then call docker cli tag command
	remoteTag, err := ch.RemoteImageTag(ctx, serviceConfig, localImageTag)
	if err != nil {
		return nil, fmt.Errorf("getting remote image tag: %w", err)
	}

	ch.publisher.Send(ctx, messaging.NewMessage(ProgressMessageKind, "Tagging container image"))
	if err := ch.docker.Tag(ctx, serviceConfig.Path(), localImageTag, remoteTag); err != nil {
		return nil, err
	}

	log.Printf("logging into container registry '%s'\n", loginServer)
	ch.publisher.Send(ctx, messaging.NewMessage(ProgressMessageKind, "Logging into container registry"))
	err = ch.containerRegistryService.Login(ctx, targetResource.SubscriptionId(), loginServer)
	if err != nil {
		return nil, err
	}

	// Push image.
	log.Printf("pushing %s to registry", remoteTag)
	ch.publisher.Send(ctx, messaging.NewMessage(ProgressMessageKind, "Pushing container image"))
	if err := ch.docker.Push(ctx, serviceConfig.Path(), remoteTag); err != nil {
		return nil, err
	}

	// Save the name of the image we pushed into the environment with a well known key.
	log.Printf("writing image name to environment")
	ch.env.SetServiceProperty(serviceConfig.Name, "IMAGE_NAME", remoteTag)

	if err := ch.env.Save(); err != nil {
		return nil, fmt.Errorf("saving image name to environment: %w", err)
	}

	return &ServiceDeployResult{
		Package: packageOutput,
	}, nil
}
