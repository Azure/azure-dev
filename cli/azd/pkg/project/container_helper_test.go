package project

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/benbjohnson/clock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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

func Test_ContainerHelper_Resolve_RegistryName(t *testing.T) {
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

func Test_ContainerHelper_Deploy(t *testing.T) {
	t.Run("Registry and private image", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockResults := setupDockerMocks(mockContext)
		env := environment.NewWithValues("dev", map[string]string{
			environment.ContainerRegistryEndpointEnvVarName: "contoso.azurecr.io",
		})
		dockerCli := docker.NewDocker(mockContext.CommandRunner)
		envManager := &mockenv.MockEnvManager{}
		envManager.On("Save", *mockContext.Context, env).Return(nil)

		mockContainerRegistryService := &mockContainerRegistryService{}
		setupContainerRegistryMocks(mockContext, &mockContainerRegistryService.Mock)

		containerHelper := NewContainerHelper(env, envManager, clock.NewMock(), mockContainerRegistryService, dockerCli)
		serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)

		packageOutput := &ServicePackageResult{
			Details: &dockerPackageResult{
				ImageHash: "1234567890",
				ImageTag:  "my-app:azd-deploy-1234567890",
			},
		}

		targetResource := environment.NewTargetResource(
			"SUBSCRIPTION_ID",
			"RESOURCE_GROUP",
			"CONTAINER_APP",
			"Microsoft.App/containerApps",
		)

		deployTask := containerHelper.Deploy(*mockContext.Context, serviceConfig, packageOutput, targetResource, true)
		logProgress(deployTask)
		deployResult, err := deployTask.Await()

		require.NoError(t, err)
		require.NotNil(t, deployResult)

		dockerDeployDetails, _ := deployResult.Details.(*dockerDeployResult)
		require.Equal(t, "contoso.azurecr.io/my-app:azd-deploy-1234567890", dockerDeployDetails.RemoteImageTag)

		_, dockerTagCalled := mockResults["docker-tag"]
		_, dockerPushCalled := mockResults["docker-push"]

		require.True(t, dockerTagCalled)
		require.True(t, dockerPushCalled)
		mockContainerRegistryService.AssertCalled(t, "Login", *mockContext.Context, "SUBSCRIPTION_ID", "contoso.azurecr.io")
	})

	t.Run("Registry and public image", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockResults := setupDockerMocks(mockContext)
		env := environment.NewWithValues("dev", map[string]string{
			environment.ContainerRegistryEndpointEnvVarName: "contoso.azurecr.io",
		})
		dockerCli := docker.NewDocker(mockContext.CommandRunner)
		envManager := &mockenv.MockEnvManager{}
		envManager.On("Save", *mockContext.Context, env).Return(nil)

		mockContainerRegistryService := &mockContainerRegistryService{}
		setupContainerRegistryMocks(mockContext, &mockContainerRegistryService.Mock)

		containerHelper := NewContainerHelper(env, envManager, clock.NewMock(), mockContainerRegistryService, dockerCli)
		serviceConfig := createTestServiceConfig("", ContainerAppTarget, ServiceLanguageDocker)

		packageOutput := &ServicePackageResult{
			Details: &dockerPackageResult{
				ImageHash: "",
				ImageTag:  "nginx",
			},
		}

		targetResource := environment.NewTargetResource(
			"SUBSCRIPTION_ID",
			"RESOURCE_GROUP",
			"CONTAINER_APP",
			"Microsoft.App/containerApps",
		)

		deployTask := containerHelper.Deploy(*mockContext.Context, serviceConfig, packageOutput, targetResource, true)
		logProgress(deployTask)
		deployResult, err := deployTask.Await()

		require.NoError(t, err)
		require.NotNil(t, deployResult)

		dockerDeployDetails, _ := deployResult.Details.(*dockerDeployResult)
		require.Equal(t, "contoso.azurecr.io/nginx", dockerDeployDetails.RemoteImageTag)

		_, dockerTagCalled := mockResults["docker-tag"]
		_, dockerPushCalled := mockResults["docker-push"]

		require.True(t, dockerTagCalled)
		require.True(t, dockerPushCalled)
		mockContainerRegistryService.AssertCalled(t, "Login", *mockContext.Context, "SUBSCRIPTION_ID", "contoso.azurecr.io")
	})

	t.Run("No registry and public image", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockResults := setupDockerMocks(mockContext)
		env := environment.NewWithValues("dev", map[string]string{})
		dockerCli := docker.NewDocker(mockContext.CommandRunner)
		envManager := &mockenv.MockEnvManager{}
		envManager.On("Save", *mockContext.Context, env).Return(nil)

		mockContainerRegistryService := &mockContainerRegistryService{}
		setupContainerRegistryMocks(mockContext, &mockContainerRegistryService.Mock)

		containerHelper := NewContainerHelper(env, envManager, clock.NewMock(), mockContainerRegistryService, dockerCli)
		serviceConfig := createTestServiceConfig("", ContainerAppTarget, ServiceLanguageDocker)

		packageOutput := &ServicePackageResult{
			Details: &dockerPackageResult{
				ImageHash: "",
				ImageTag:  "nginx",
			},
		}

		targetResource := environment.NewTargetResource(
			"SUBSCRIPTION_ID",
			"RESOURCE_GROUP",
			"CONTAINER_APP",
			"Microsoft.App/containerApps",
		)

		deployTask := containerHelper.Deploy(*mockContext.Context, serviceConfig, packageOutput, targetResource, true)
		logProgress(deployTask)
		deployResult, err := deployTask.Await()

		require.NoError(t, err)
		require.NotNil(t, deployResult)

		dockerDeployDetails, _ := deployResult.Details.(*dockerDeployResult)
		require.Equal(t, "nginx", dockerDeployDetails.RemoteImageTag)

		_, dockerTagCalled := mockResults["docker-tag"]
		_, dockerPushCalled := mockResults["docker-push"]

		require.False(t, dockerTagCalled)
		require.False(t, dockerPushCalled)
		mockContainerRegistryService.AssertNotCalled(t, "Login")
	})

	t.Run("Code without registry", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		env := environment.NewWithValues("dev", map[string]string{})
		dockerCli := docker.NewDocker(mockContext.CommandRunner)
		envManager := &mockenv.MockEnvManager{}
		envManager.On("Save", *mockContext.Context, env).Return(nil)

		mockContainerRegistryService := &mockContainerRegistryService{}
		setupContainerRegistryMocks(mockContext, &mockContainerRegistryService.Mock)

		containerHelper := NewContainerHelper(env, envManager, clock.NewMock(), mockContainerRegistryService, dockerCli)
		serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)

		targetResource := environment.NewTargetResource(
			"SUBSCRIPTION_ID",
			"RESOURCE_GROUP",
			"CONTAINER_APP",
			"Microsoft.App/containerApps",
		)

		deployTask := containerHelper.Deploy(*mockContext.Context, serviceConfig, nil, targetResource, true)
		logProgress(deployTask)
		deployResult, err := deployTask.Await()

		// Expected to fail when no registry is specified
		require.Error(t, err)
		require.Nil(t, deployResult)
	})
}

func setupContainerRegistryMocks(mockContext *mocks.MockContext, mockContainerRegistryService *mock.Mock) {
	mockContainerRegistryService.On(
		"Login",
		*mockContext.Context,
		mock.AnythingOfType("string"),
		mock.AnythingOfType("string")).
		Return(nil)
}

func setupDockerMocks(mockContext *mocks.MockContext) map[string]exec.RunArgs {
	mockResults := map[string]exec.RunArgs{}

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "docker tag")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		mockResults["docker-tag"] = args
		return exec.NewRunResult(0, "", ""), nil
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "docker push")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		mockResults["docker-push"] = args
		return exec.NewRunResult(0, "", ""), nil
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "docker login")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		mockResults["docker-login"] = args
		return exec.NewRunResult(0, "", ""), nil
	})

	return mockResults
}

type mockContainerRegistryService struct {
	mock.Mock
}

func (m *mockContainerRegistryService) Login(ctx context.Context, subscriptionId string, loginServer string) error {
	args := m.Called(ctx, subscriptionId, loginServer)
	return args.Error(0)
}

func (m *mockContainerRegistryService) Credentials(
	ctx context.Context,
	subscriptionId string,
	loginServer string,
) (*azcli.DockerCredentials, error) {
	args := m.Called(ctx, subscriptionId, loginServer)
	return args.Get(0).(*azcli.DockerCredentials), args.Error(1)
}

func (m *mockContainerRegistryService) GetContainerRegistries(
	ctx context.Context,
	subscriptionId string,
) ([]*armcontainerregistry.Registry, error) {
	args := m.Called(ctx, subscriptionId)
	return args.Get(0).([]*armcontainerregistry.Registry), args.Error(1)
}
