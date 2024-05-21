package project

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/benbjohnson/clock"
)

type ImageHelper struct {
	env   *environment.Environment
	clock clock.Clock
}

func NewImageHelper(
	env *environment.Environment,
	clock clock.Clock,
) *ImageHelper {
	return &ImageHelper{
		env:   env,
		clock: clock,
	}
}

// DefaultImageName returns a default image name generated from the service name and environment name.
func (ih *ImageHelper) DefaultImageName(serviceConfig *ServiceConfig) string {
	return fmt.Sprintf("%s/%s-%s",
		strings.ToLower(serviceConfig.Project.Name),
		strings.ToLower(serviceConfig.Name),
		strings.ToLower(ih.env.Name()))
}

// DefaultImageTag returns a default image tag generated from the current time.
func (ih *ImageHelper) DefaultImageTag() string {
	return fmt.Sprintf("azd-deploy-%d", ih.clock.Now().Unix())
}

// RegistryName returns the name of the destination container registry to use for the current environment from the following:
// 1. AZURE_CONTAINER_REGISTRY_ENDPOINT environment variable
// 2. docker.registry from the service configuration
func (ih *ImageHelper) RegistryName(ctx context.Context, serviceConfig *ServiceConfig) (string, error) {
	registryName, found := ih.env.LookupEnv(environment.ContainerRegistryEndpointEnvVarName)
	if !found {
		log.Printf(
			"Container registry not found in '%s' environment variable\n",
			environment.ContainerRegistryEndpointEnvVarName,
		)
	}

	if registryName == "" {
		yamlRegistryName, err := serviceConfig.Docker.Registry.Envsubst(ih.env.Getenv)
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
func (ih *ImageHelper) GeneratedImage(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) (*docker.ContainerImage, error) {
	// Parse the image from azure.yaml configuration when available
	configuredImage, err := serviceConfig.Docker.Image.Envsubst(ih.env.Getenv)
	if err != nil {
		return nil, fmt.Errorf("failed parsing 'image' from docker configuration, %w", err)
	}

	// Set default image name if not configured
	if configuredImage == "" {
		configuredImage = ih.DefaultImageName(serviceConfig)
	}

	parsedImage, err := docker.ParseContainerImage(configuredImage)
	if err != nil {
		return nil, fmt.Errorf("failed parsing configured image, %w", err)
	}

	if parsedImage.Tag == "" {
		configuredTag, err := serviceConfig.Docker.Tag.Envsubst(ih.env.Getenv)
		if err != nil {
			return nil, fmt.Errorf("failed parsing 'tag' from docker configuration, %w", err)
		}

		// Set default tag if not configured
		if configuredTag == "" {
			configuredTag = ih.DefaultImageTag()
		}

		parsedImage.Tag = configuredTag
	}

	// Set default registry if not configured
	if parsedImage.Registry == "" {
		// This can fail if called before provisioning the registry
		configuredRegistry, err := ih.RegistryName(ctx, serviceConfig)
		if err == nil {
			parsedImage.Registry = configuredRegistry
		}
	}

	return parsedImage, nil
}

// RemoteImageTag returns the remote image tag for the service configuration.
func (ih *ImageHelper) RemoteImageTag(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	localImageTag string,
) (string, error) {
	registryName, err := ih.RegistryName(ctx, serviceConfig)
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
func (ih *ImageHelper) LocalImageTag(ctx context.Context, serviceConfig *ServiceConfig) (string, error) {
	configuredImage, err := ih.GeneratedImage(ctx, serviceConfig)
	if err != nil {
		return "", err
	}

	return configuredImage.Local(), nil
}
