package project

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/benbjohnson/clock"
	"github.com/sethvargo/go-retry"
)

type ContainerHelper struct {
	env                      *environment.Environment
	envManager               environment.Manager
	containerRegistryService azcli.ContainerRegistryService
	docker                   docker.Docker
	clock                    clock.Clock
	cloud                    *cloud.Cloud
}

func NewContainerHelper(
	env *environment.Environment,
	envManager environment.Manager,
	clock clock.Clock,
	containerRegistryService azcli.ContainerRegistryService,
	docker docker.Docker,
	cloud *cloud.Cloud,
) *ContainerHelper {
	return &ContainerHelper{
		env:                      env,
		envManager:               envManager,
		containerRegistryService: containerRegistryService,
		docker:                   docker,
		clock:                    clock,
		cloud:                    cloud,
	}
}

// DefaultImageName returns a default image name generated from the service name and environment name.
func (ch *ContainerHelper) DefaultImageName(serviceConfig *ServiceConfig) string {
	return fmt.Sprintf("%s/%s-%s",
		strings.ToLower(serviceConfig.Project.Name),
		strings.ToLower(serviceConfig.Name),
		strings.ToLower(ch.env.Name()))
}

// DefaultImageTag returns a default image tag generated from the current time.
func (ch *ContainerHelper) DefaultImageTag() string {
	return fmt.Sprintf("azd-deploy-%d", ch.clock.Now().Unix())
}

// RegistryName returns the name of the destination container registry to use for the current environment from the following:
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

	// If the service provides its own code artifacts then the expectation is that an images needs to be built and
	// pushed to a container registry.
	// If the service does not provide its own code artifacts then the expectation is a registry is optional and
	// an image can be referenced independently.
	if serviceConfig.RelativePath != "" && registryName == "" {
		return "", fmt.Errorf(
			//nolint:lll
			"could not determine container registry endpoint, ensure 'registry' has been set in the docker options or '%s' environment variable has been set",
			environment.ContainerRegistryEndpointEnvVarName,
		)
	}

	return registryName, nil
}

// GeneratedImage returns the configured image from the service configuration
// or a default image name generated from the service name and environment name.
func (ch *ContainerHelper) GeneratedImage(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) (*docker.ContainerImage, error) {
	// Parse the image from azure.yaml configuration when available
	configuredImage, err := serviceConfig.Docker.Image.Envsubst(ch.env.Getenv)
	if err != nil {
		return nil, fmt.Errorf("failed parsing 'image' from docker configuration, %w", err)
	}

	// Set default image name if not configured
	if configuredImage == "" {
		configuredImage = ch.DefaultImageName(serviceConfig)
	}

	parsedImage, err := docker.ParseContainerImage(configuredImage)
	if err != nil {
		return nil, fmt.Errorf("failed parsing configured image, %w", err)
	}

	if parsedImage.Tag == "" {
		configuredTag, err := serviceConfig.Docker.Tag.Envsubst(ch.env.Getenv)
		if err != nil {
			return nil, fmt.Errorf("failed parsing 'tag' from docker configuration, %w", err)
		}

		// Set default tag if not configured
		if configuredTag == "" {
			configuredTag = ch.DefaultImageTag()
		}

		parsedImage.Tag = configuredTag
	}

	// Set default registry if not configured
	if parsedImage.Registry == "" {
		// This can fail if called before provisioning the registry
		configuredRegistry, err := ch.RegistryName(ctx, serviceConfig)
		if err == nil {
			parsedImage.Registry = configuredRegistry
		}
	}

	return parsedImage, nil
}

// RemoteImageTag returns the remote image tag for the service configuration.
func (ch *ContainerHelper) RemoteImageTag(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	localImageTag string,
) (string, error) {
	registryName, err := ch.RegistryName(ctx, serviceConfig)
	if err != nil {
		return "", err
	}

	containerImage, err := docker.ParseContainerImage(localImageTag)
	if err != nil {
		return "", err
	}

	if registryName != "" {
		containerImage.Registry = registryName
	}

	return containerImage.Remote(), nil
}

// LocalImageTag returns the local image tag for the service configuration.
func (ch *ContainerHelper) LocalImageTag(ctx context.Context, serviceConfig *ServiceConfig) (string, error) {
	configuredImage, err := ch.GeneratedImage(ctx, serviceConfig)
	if err != nil {
		return "", err
	}

	return configuredImage.Local(), nil
}

func (ch *ContainerHelper) RequiredExternalTools(context.Context) []tools.ExternalTool {
	return []tools.ExternalTool{ch.docker}
}

// Login logs into the container registry specified by AZURE_CONTAINER_REGISTRY_ENDPOINT in the environment. On success,
// it returns the name of the container registry that was logged into.
func (ch *ContainerHelper) Login(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) (string, error) {
	registryName, err := ch.RegistryName(ctx, serviceConfig)
	if err != nil {
		return "", err
	}

	// Only perform automatic login for ACR
	// Other registries require manual login via external 'docker login' command
	hostParts := strings.Split(registryName, ".")
	if len(hostParts) == 1 || strings.HasSuffix(registryName, ch.cloud.ContainerRegistryEndpointSuffix) {
		return registryName, ch.containerRegistryService.Login(ctx, ch.env.GetSubscriptionId(), registryName)
	}

	return registryName, nil
}

var defaultCredentialsRetryDelay = 20 * time.Second

func (ch *ContainerHelper) Credentials(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) (*azcli.DockerCredentials, error) {
	loginServer, err := ch.RegistryName(ctx, serviceConfig)
	if err != nil {
		return nil, err
	}

	var credential *azcli.DockerCredentials
	credentialsError := retry.Do(
		ctx,
		// will retry just once after 1 minute based on:
		// https://learn.microsoft.com/en-us/azure/dns/dns-faq#how-long-does-it-take-for-dns-changes-to-take-effect-
		retry.WithMaxRetries(3, retry.NewConstant(defaultCredentialsRetryDelay)),
		func(ctx context.Context) error {
			cred, err := ch.containerRegistryService.Credentials(ctx, targetResource.SubscriptionId(), loginServer)
			if err != nil {
				var httpErr *azcore.ResponseError
				if errors.As(err, &httpErr) {
					if httpErr.StatusCode == 404 {
						// Retry if the registry is not found while logging in
						return retry.RetryableError(err)
					}
				}
				return err
			}
			credential = cred
			return nil
		})

	return credential, credentialsError
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
			registryName, err := ch.RegistryName(ctx, serviceConfig)
			if err != nil {
				task.SetError(err)
				return
			}

			var sourceImage string
			targetImage := packageOutput.PackagePath

			packageDetails, ok := packageOutput.Details.(*dockerPackageResult)
			if ok && packageDetails != nil {
				sourceImage = packageDetails.SourceImage
				targetImage = packageDetails.TargetImage
			}

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

					_, err = ch.Login(ctx, serviceConfig)
					if err != nil {
						task.SetError(err)
						return
					}

					// Push image.
					log.Printf("pushing %s to registry", remoteImage)
					task.SetProgress(NewServiceProgress("Pushing container image"))
					if err := ch.docker.Push(ctx, serviceConfig.Path(), remoteImage); err != nil {
						errSuggestion := &internal.ErrorWithSuggestion{
							Err: err,
							//nolint:lll
							Suggestion: "When pushing to an external registry, ensure you have successfully authenticated by calling 'docker login' and run 'azd deploy' again",
						}

						task.SetError(errSuggestion)
						return
					}
				}
			}

			if writeImageToEnv {
				// Save the name of the image we pushed into the environment with a well known key.
				log.Printf("writing image name to environment")
				ch.env.SetServiceProperty(serviceConfig.Name, "IMAGE_NAME", remoteImage)

				if err := ch.envManager.Save(ctx, ch.env); err != nil {
					task.SetError(fmt.Errorf("saving image name to environment: %w", err))
					return
				}
			}

			task.SetResult(&ServiceDeployResult{
				Package: packageOutput,
				Details: &dockerDeployResult{
					RemoteImageTag: remoteImage,
				},
			})
		})
}

type dockerDeployResult struct {
	RemoteImageTag string
}
