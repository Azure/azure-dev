package project

import (
	"context"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
)

// RegistryName returns the name of the destination container registry to use for the current environment from the following:
// 1. AZURE_CONTAINER_REGISTRY_ENDPOINT environment variable
// 2. docker.registry from the service configuration
func RegistryName(ctx context.Context, env *environment.Environment, serviceConfig *ServiceConfig) (string, error) {
	registryName, found := env.LookupEnv(environment.ContainerRegistryEndpointEnvVarName)
	if !found {
		log.Printf(
			"Container registry not found in '%s' environment variable\n",
			environment.ContainerRegistryEndpointEnvVarName,
		)
	}

	if registryName == "" {
		yamlRegistryName, err := serviceConfig.Docker.Registry.Envsubst(env.Getenv)
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
