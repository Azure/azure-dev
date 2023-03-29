package project

import (
	"context"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
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
			env := environment.EphemeralWithValues("dev", map[string]string{})
			containerHelper := NewContainerHelper(env, clock.NewMock())
			serviceConfig.Docker = tt.dockerConfig

			tag, err := containerHelper.LocalImageTag(*mockContext.Context, serviceConfig)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, tag)
		})
	}
}

func Test_ContainerHelper_RemoteImageTag(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	env := environment.EphemeralWithValues("dev", map[string]string{
		environment.ContainerRegistryEndpointEnvVarName: "contoso.azurecr.io",
	})
	containerHelper := NewContainerHelper(env, clock.NewMock())
	serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)
	remoteTag, err := containerHelper.RemoteImageTag(*mockContext.Context, serviceConfig)
	require.NoError(t, err)
	require.Equal(t, "contoso.azurecr.io/test-app/api-dev:azd-deploy-0", remoteTag)
}

func Test_ContainerHelper_RemoteImageTag_NoContainer_Registry(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	env := environment.Ephemeral()
	serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)
	containerHelper := NewContainerHelper(env, clock.NewMock())

	imageTag, err := containerHelper.RemoteImageTag(*mockContext.Context, serviceConfig)
	require.Error(t, err)
	require.Empty(t, imageTag)
}
