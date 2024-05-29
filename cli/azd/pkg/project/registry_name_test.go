package project

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_RegistryName(t *testing.T) {
	t.Run("Default EnvVar", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		env := environment.NewWithValues("dev", map[string]string{
			environment.ContainerRegistryEndpointEnvVarName: "contoso.azurecr.io",
		})
		serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)
		registryName, err := RegistryName(*mockContext.Context, env, serviceConfig)

		require.NoError(t, err)
		require.Equal(t, "contoso.azurecr.io", registryName)
	})

	t.Run("Azure Yaml with simple string", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		env := environment.NewWithValues("dev", map[string]string{})
		serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)
		serviceConfig.Docker.Registry = osutil.NewExpandableString("contoso.azurecr.io")
		registryName, err := RegistryName(*mockContext.Context, env, serviceConfig)

		require.NoError(t, err)
		require.Equal(t, "contoso.azurecr.io", registryName)
	})

	t.Run("Azure Yaml with expandable string", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		env := environment.NewWithValues("dev", map[string]string{})
		env.DotenvSet("MY_CUSTOM_REGISTRY", "custom.azurecr.io")
		serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)
		serviceConfig.Docker.Registry = osutil.NewExpandableString("${MY_CUSTOM_REGISTRY}")
		registryName, err := RegistryName(*mockContext.Context, env, serviceConfig)

		require.NoError(t, err)
		require.Equal(t, "custom.azurecr.io", registryName)
	})

	t.Run("No registry name", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		env := environment.NewWithValues("dev", map[string]string{})
		serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)
		registryName, err := RegistryName(*mockContext.Context, env, serviceConfig)

		require.Error(t, err)
		require.Empty(t, registryName)
	})
}
