package project

import (
	"context"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/benbjohnson/clock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_ContainerHelper_LocalImageTag(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockClock := clock.NewMock()
	envName := "dev"
	projectName := "my-app"
	serviceName := "web"
	serviceConfig := &ServiceConfig{
		Name: serviceName,
		Host: "containerapp",
		Project: &ProjectConfig{
			Name: projectName,
		},
	}
	defaultImageName := fmt.Sprintf("%s/%s-%s", projectName, serviceName, envName)

	envManager := &mockenv.MockEnvManager{}

	tests := []struct {
		name         string
		dockerConfig DockerProjectOptions
		want         string
	}{
		{
			"Default",
			DockerProjectOptions{},
			fmt.Sprintf("%s:azd-deploy-%d", defaultImageName, mockClock.Now().Unix())},
		{
			"ImageTagSpecified",
			DockerProjectOptions{
				Tag: NewExpandableString("contoso/contoso-image:latest"),
			},
			"contoso/contoso-image:latest"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := environment.NewWithValues("dev", map[string]string{})
			containerHelper := NewContainerHelper(env, envManager, clock.NewMock(), nil, nil)
			serviceConfig.Docker = tt.dockerConfig

			tag, err := containerHelper.LocalImageTag(*mockContext.Context, serviceConfig)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, tag)
		})
	}
}

func Test_ContainerHelper_RemoteImageTag(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	env := environment.NewWithValues("dev", map[string]string{
		environment.ContainerRegistryEndpointEnvVarName: "contoso.azurecr.io",
	})
	envManager := &mockenv.MockEnvManager{}
	containerHelper := NewContainerHelper(env, envManager, clock.NewMock(), nil, nil)
	serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)
	localTag, err := containerHelper.LocalImageTag(*mockContext.Context, serviceConfig)
	require.NoError(t, err)
	remoteTag, err := containerHelper.RemoteImageTag(*mockContext.Context, serviceConfig, localTag)
	require.NoError(t, err)
	require.Equal(t, "contoso.azurecr.io/test-app/api-dev:azd-deploy-0", remoteTag)
}

func Test_ContainerHelper_RemoteImageTag_NoContainer_Registry(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	env := environment.New("test")
	serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)
	envManager := &mockenv.MockEnvManager{}
	containerHelper := NewContainerHelper(env, envManager, clock.NewMock(), nil, nil)

	imageTag, err := containerHelper.RemoteImageTag(*mockContext.Context, serviceConfig, "local_tag")
	require.Error(t, err)
	require.Empty(t, imageTag)
}

func Test_Resolve_RegistryName(t *testing.T) {
	t.Run("Default EnvVar", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		env := environment.NewWithValues("dev", map[string]string{
			environment.ContainerRegistryEndpointEnvVarName: "contoso.azurecr.io",
		})
		envManager := &mockenv.MockEnvManager{}
		containerHelper := NewContainerHelper(env, envManager, clock.NewMock(), nil, nil)
		serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)
		registryName, err := containerHelper.RegistryName(*mockContext.Context, serviceConfig)

		require.NoError(t, err)
		require.Equal(t, "contoso.azurecr.io", registryName)
	})

	t.Run("Azure Yaml with simple string", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		env := environment.NewWithValues("dev", map[string]string{})
		envManager := &mockenv.MockEnvManager{}
		containerHelper := NewContainerHelper(env, envManager, clock.NewMock(), nil, nil)
		serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)
		serviceConfig.Docker.Registry = NewExpandableString("contoso.azurecr.io")
		registryName, err := containerHelper.RegistryName(*mockContext.Context, serviceConfig)

		require.NoError(t, err)
		require.Equal(t, "contoso.azurecr.io", registryName)
	})

	t.Run("Azure Yaml with expandable string", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		env := environment.NewWithValues("dev", map[string]string{})
		env.DotenvSet("MY_CUSTOM_REGISTRY", "custom.azurecr.io")
		envManager := &mockenv.MockEnvManager{}
		containerHelper := NewContainerHelper(env, envManager, clock.NewMock(), nil, nil)
		serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)
		serviceConfig.Docker.Registry = NewExpandableString("${MY_CUSTOM_REGISTRY}")
		registryName, err := containerHelper.RegistryName(*mockContext.Context, serviceConfig)

		require.NoError(t, err)
		require.Equal(t, "custom.azurecr.io", registryName)
	})

	t.Run("No registry name", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		env := environment.NewWithValues("dev", map[string]string{})
		envManager := &mockenv.MockEnvManager{}
		containerHelper := NewContainerHelper(env, envManager, clock.NewMock(), nil, nil)
		serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)
		registryName, err := containerHelper.RegistryName(*mockContext.Context, serviceConfig)

		require.Error(t, err)
		require.Empty(t, registryName)
	})
}
