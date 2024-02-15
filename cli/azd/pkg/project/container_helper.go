package project

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/benbjohnson/clock"
)

type ContainerHelper struct {
	env                      *environment.Environment
	envManager               environment.Manager
	containerRegistryService azcli.ContainerRegistryService
	docker                   docker.Docker
	clock                    clock.Clock
}

func NewContainerHelper(
	env *environment.Environment,
	envManager environment.Manager,
	clock clock.Clock,
	containerRegistryService azcli.ContainerRegistryService,
	docker docker.Docker,
) *ContainerHelper {
	return &ContainerHelper{
		env:                      env,
		envManager:               envManager,
		containerRegistryService: containerRegistryService,
		docker:                   docker,
		clock:                    clock,
	}
}

// RegistryName returns the name of the container registry to use for the current environment from the following:
// 1. AZURE_CONTAINER_REGISTRY_ENDPOINT environment variable
// 2. docker.registry from the service configuration
func (ch *ContainerHelper) RegistryName(ctx context.Context, serviceConfig *ServiceConfig) (string, error) {
	registryName, found := ch.env.LookupEnv(environment.ContainerRegistryEndpointEnvVarName)
	if !found {
		log.Printf(
			"Container registry not found in '%s' environment variable\n",
			environment.ContainerRegistryEndpointEnvVarName,
		)
	}

	if registryName == "" {
		yamlRegistryName, err := serviceConfig.Docker.Registry.Envsubst(ch.env.Getenv)
		if err != nil {
			log.Println("Failed expanding 'docker.registry'")
		}

		registryName = yamlRegistryName
	}

	if registryName == "" {
		return "", fmt.Errorf(
			//nolint:lll
			"could not determine container registry endpoint, ensure 'registry' has been set in the docker options or '%s' environment variable has been set",
			environment.ContainerRegistryEndpointEnvVarName,
		)
	}

	return registryName, nil
}

func (ch *ContainerHelper) RemoteImageTag(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	localImageTag string,
) (string, error) {
	loginServer, err := ch.RegistryName(ctx, serviceConfig)
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
		strings.ToLower(ch.env.Name()),
		ch.clock.Now().Unix(),
	), nil
}

func (ch *ContainerHelper) RequiredExternalTools(context.Context) []tools.ExternalTool {
	return []tools.ExternalTool{ch.docker}
}

// Login logs into the container registry specified by AZURE_CONTAINER_REGISTRY_ENDPOINT in the environment. On success,
// it returns the name of the container registry that was logged into.
func (ch *ContainerHelper) Login(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) (string, error) {
	loginServer, err := ch.RegistryName(ctx, serviceConfig)
	if err != nil {
		return "", err
	}

	return loginServer, ch.containerRegistryService.Login(ctx, targetResource.SubscriptionId(), loginServer)
}

func (ch *ContainerHelper) Credentials(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) (*azcli.DockerCredentials, error) {
	loginServer, err := ch.RegistryName(ctx, serviceConfig)
	if err != nil {
		return nil, err
	}

	return ch.containerRegistryService.Credentials(ctx, targetResource.SubscriptionId(), loginServer)
}

// Deploy pushes and image to a remote server, and optionally writes the fully qualified remote image name to the
// environment on success.
func (ch *ContainerHelper) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
	targetResource *environment.TargetResource,
	writeImageToEnv bool,
) *async.TaskWithProgress[*ServiceDeployResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServiceDeployResult, ServiceProgress]) {
			// Get ACR Login Server
			loginServer, err := ch.RegistryName(ctx, serviceConfig)
			if err != nil {
				task.SetError(err)
				return
			}

			localImageTag := packageOutput.PackagePath
			packageDetails, ok := packageOutput.Details.(*dockerPackageResult)
			if ok && packageDetails != nil {
				localImageTag = packageDetails.ImageTag
			}

<<<<<<< HEAD
			// Default to the local image tag
			remoteImage := targetImage

			// If we don't have a registry specified and the service does not reference a project path
			// then we are referencing a public/pre-existing image and don't have anything to tag or push
			if registryName == "" && serviceConfig.RelativePath == "" && sourceImage != "" {
				remoteImage = sourceImage
			} else {
				if targetImage == "" {
					task.SetError(errors.New("failed retrieving package result details"))
					return
				}

				// If a registry has not been defined then there is no need to tag or push any images
				if registryName != "" {
					// When the project does not contain source and we are using an external image we first need to pull the image
					// before we're able to push it to a remote registry
					// In most cases this pull will have already been part of the package step
					if packageDetails != nil && serviceConfig.RelativePath == "" {
						task.SetProgress(NewServiceProgress("Pulling container image"))
						err = ch.docker.Pull(ctx, sourceImage)
						if err != nil {
							task.SetError(fmt.Errorf("pulling image: %w", err))
							return
						}
					}

					// Tag image
					// Get remote remoteImageWithTag from the container helper then call docker cli remoteImageWithTag command
					remoteImageWithTag, err := ch.RemoteImageTag(ctx, serviceConfig, targetImage)
					if err != nil {
						task.SetError(fmt.Errorf("getting remote image tag: %w", err))
						return
					}

					remoteImage = remoteImageWithTag

					task.SetProgress(NewServiceProgress("Tagging container image"))
					if err := ch.docker.Tag(ctx, serviceConfig.Path(), targetImage, remoteImage); err != nil {
						task.SetError(err)
						return
					}

					log.Printf("logging into container registry '%s'\n", registryName)
					task.SetProgress(NewServiceProgress("Logging into container registry"))
					err = ch.containerRegistryService.Login(ctx, targetResource.SubscriptionId(), registryName)
					if err != nil {
						task.SetError(err)
						return
					}

					// Push image.
					log.Printf("pushing %s to registry", remoteImage)
					task.SetProgress(NewServiceProgress("Pushing container image"))
					if err := ch.docker.Push(ctx, serviceConfig.Path(), remoteImage); err != nil {
						task.SetError(err)
						return
					}
				}
=======
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

			task.SetProgress(NewServiceProgress("Tagging container image"))
			if err := ch.docker.Tag(ctx, serviceConfig.Path(), localImageTag, remoteTag); err != nil {
				task.SetError(err)
				return
			}

			log.Printf("logging into container registry '%s'\n", loginServer)
			task.SetProgress(NewServiceProgress("Logging into container registry"))
			err = ch.containerRegistryService.Login(ctx, targetResource.SubscriptionId(), loginServer)
			if err != nil {
				task.SetError(err)
				return
			}

			// Push image.
			log.Printf("pushing %s to registry", remoteTag)
			task.SetProgress(NewServiceProgress("Pushing container image"))
			if err := ch.docker.Push(ctx, serviceConfig.Path(), remoteTag); err != nil {
				task.SetError(err)
				return
>>>>>>> 277296b8 (Revert "Merge branch 'Azure:main' into helloai")
			}

			if writeImageToEnv {
				// Save the name of the image we pushed into the environment with a well known key.
				log.Printf("writing image name to environment")
				ch.env.SetServiceProperty(serviceConfig.Name, "IMAGE_NAME", remoteTag)

				if err := ch.envManager.Save(ctx, ch.env); err != nil {
					task.SetError(fmt.Errorf("saving image name to environment: %w", err))
					return
				}
			}

			task.SetResult(&ServiceDeployResult{
				Package: packageOutput,
				Details: &dockerDeployResult{
					RemoteImageTag: remoteTag,
				},
			})
		})
}

type dockerDeployResult struct {
	RemoteImageTag string
}
