package project

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
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
				Image: osutil.NewExpandableString("contoso/contoso-image:latest"),
			},
			"contoso/contoso-image:latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := environment.NewWithValues("dev", map[string]string{})
			containerHelper := NewContainerHelper(env, nil, clock.NewMock(), nil, nil, nil, nil, nil, cloud.AzurePublic())
			serviceConfig.Docker = tt.dockerConfig

			tag, err := containerHelper.LocalImageTag(*mockContext.Context, serviceConfig)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, tag)
		})
	}
}

func Test_ContainerHelper_RemoteImageTag(t *testing.T) {
	tests := []struct {
		name              string
		project           string
		localImageTag     string
		registry          osutil.ExpandableString
		expectedRemoteTag string
		expectError       bool
	}{
		{
			name:              "with registry",
			project:           "./src/api",
			registry:          osutil.NewExpandableString("contoso.azurecr.io"),
			localImageTag:     "test-app/api-dev:azd-deploy-0",
			expectedRemoteTag: "contoso.azurecr.io/test-app/api-dev:azd-deploy-0",
		},
		{
			name:              "with no registry and custom image",
			project:           "",
			localImageTag:     "docker.io/org/my-custom-image:latest",
			expectedRemoteTag: "docker.io/org/my-custom-image:latest",
		},
		{
			name:              "registry with fully qualified custom image",
			project:           "",
			registry:          osutil.NewExpandableString("contoso.azurecr.io"),
			localImageTag:     "docker.io/org/my-custom-image:latest",
			expectedRemoteTag: "contoso.azurecr.io/org/my-custom-image:latest",
		},
		{
			name:          "missing registry with local project",
			project:       "./src/api",
			localImageTag: "test-app/api-dev:azd-deploy-0",
			expectError:   true,
		},
	}

	mockContext := mocks.NewMockContext(context.Background())
	env := environment.NewWithValues("dev", map[string]string{})
	containerHelper := NewContainerHelper(env, nil, clock.NewMock(), nil, nil, nil, nil, nil, cloud.AzurePublic())

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serviceConfig := createTestServiceConfig(tt.project, ContainerAppTarget, ServiceLanguageTypeScript)
			serviceConfig.Docker.Registry = tt.registry

			remoteTag, err := containerHelper.RemoteImageTag(*mockContext.Context, serviceConfig, tt.localImageTag)

			if tt.expectError {
				require.Error(t, err)
				require.Empty(t, remoteTag)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedRemoteTag, remoteTag)
			}
		})
	}
}

func Test_ContainerHelper_Resolve_RegistryName(t *testing.T) {
	t.Run("Default EnvVar", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		env := environment.NewWithValues("dev", map[string]string{
			environment.ContainerRegistryEndpointEnvVarName: "contoso.azurecr.io",
		})
		envManager := &mockenv.MockEnvManager{}
		containerHelper := NewContainerHelper(env, envManager, clock.NewMock(), nil, nil, nil, nil, nil, cloud.AzurePublic())
		serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)
		registryName, err := containerHelper.RegistryName(*mockContext.Context, serviceConfig)

		require.NoError(t, err)
		require.Equal(t, "contoso.azurecr.io", registryName)
	})

	t.Run("Azure Yaml with simple string", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		env := environment.NewWithValues("dev", map[string]string{})
		envManager := &mockenv.MockEnvManager{}
		containerHelper := NewContainerHelper(env, envManager, clock.NewMock(), nil, nil, nil, nil, nil, cloud.AzurePublic())
		serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)
		serviceConfig.Docker.Registry = osutil.NewExpandableString("contoso.azurecr.io")
		registryName, err := containerHelper.RegistryName(*mockContext.Context, serviceConfig)

		require.NoError(t, err)
		require.Equal(t, "contoso.azurecr.io", registryName)
	})

	t.Run("Azure Yaml with expandable string", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		env := environment.NewWithValues("dev", map[string]string{})
		env.DotenvSet("MY_CUSTOM_REGISTRY", "custom.azurecr.io")
		envManager := &mockenv.MockEnvManager{}
		containerHelper := NewContainerHelper(env, envManager, clock.NewMock(), nil, nil, nil, nil, nil, cloud.AzurePublic())
		serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)
		serviceConfig.Docker.Registry = osutil.NewExpandableString("${MY_CUSTOM_REGISTRY}")
		registryName, err := containerHelper.RegistryName(*mockContext.Context, serviceConfig)

		require.NoError(t, err)
		require.Equal(t, "custom.azurecr.io", registryName)
	})

	t.Run("No registry name", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		env := environment.NewWithValues("dev", map[string]string{})
		envManager := &mockenv.MockEnvManager{}
		containerHelper := NewContainerHelper(env, envManager, clock.NewMock(), nil, nil, nil, nil, nil, cloud.AzurePublic())
		serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)
		registryName, err := containerHelper.RegistryName(*mockContext.Context, serviceConfig)

		require.Error(t, err)
		require.Empty(t, registryName)
	})
}

func Test_ContainerHelper_Deploy(t *testing.T) {
	tests := []struct {
		name                    string
		registry                osutil.ExpandableString
		image                   string
		project                 string
		packagePath             string
		dockerDetails           *dockerPackageResult
		expectedRemoteImage     string
		expectDockerLoginCalled bool
		expectDockerPullCalled  bool
		expectDockerTagCalled   bool
		expectDockerPushCalled  bool
		expectError             bool
	}{
		{
			name:     "Source code and registry",
			project:  "./src/api",
			registry: osutil.NewExpandableString("contoso.azurecr.io"),
			dockerDetails: &dockerPackageResult{
				ImageHash:   "IMAGE_ID",
				SourceImage: "",
				TargetImage: "my-project/my-service:azd-deploy-0",
			},
			expectDockerLoginCalled: true,
			expectDockerPullCalled:  false,
			expectDockerTagCalled:   true,
			expectDockerPushCalled:  true,
			expectedRemoteImage:     "contoso.azurecr.io/my-project/my-service:azd-deploy-0",
			expectError:             false,
		},
		{
			name:    "Source code and no registry",
			project: "./src/api",
			dockerDetails: &dockerPackageResult{
				ImageHash:   "IMAGE_ID",
				SourceImage: "",
				TargetImage: "my-project/my-service:azd-deploy-0",
			},
			expectError:             true,
			expectDockerLoginCalled: false,
			expectDockerPullCalled:  false,
			expectDockerTagCalled:   false,
			expectDockerPushCalled:  false,
		},
		{
			name:                    "Source code with existing package path",
			project:                 "./src/api",
			registry:                osutil.NewExpandableString("contoso.azurecr.io"),
			packagePath:             "my-project/my-service:azd-deploy-0",
			expectedRemoteImage:     "contoso.azurecr.io/my-project/my-service:azd-deploy-0",
			expectDockerLoginCalled: true,
			expectDockerPullCalled:  false,
			expectDockerTagCalled:   true,
			expectDockerPushCalled:  true,
			expectError:             false,
		},
		{
			name:     "Source image and registry",
			image:    "nginx",
			registry: osutil.NewExpandableString("contoso.azurecr.io"),
			dockerDetails: &dockerPackageResult{
				ImageHash:   "",
				SourceImage: "nginx",
				TargetImage: "my-project/nginx:azd-deploy-0",
			},
			expectDockerLoginCalled: true,
			expectDockerPullCalled:  true,
			expectDockerTagCalled:   true,
			expectDockerPushCalled:  true,
			expectedRemoteImage:     "contoso.azurecr.io/my-project/nginx:azd-deploy-0",
			expectError:             false,
		},
		{
			name:     "Source image and external registry",
			image:    "nginx",
			registry: osutil.NewExpandableString("docker.io/custom"),
			dockerDetails: &dockerPackageResult{
				ImageHash:   "",
				SourceImage: "nginx",
				TargetImage: "my-project/nginx:azd-deploy-0",
			},
			expectDockerLoginCalled: false,
			expectDockerPullCalled:  true,
			expectDockerTagCalled:   true,
			expectDockerPushCalled:  true,
			expectedRemoteImage:     "docker.io/custom/my-project/nginx:azd-deploy-0",
			expectError:             false,
		},
		{
			name:  "Source image and no registry",
			image: "nginx",
			dockerDetails: &dockerPackageResult{
				ImageHash:   "",
				SourceImage: "nginx",
				TargetImage: "my-project/nginx:azd-deploy-0",
			},
			expectDockerLoginCalled: false,
			expectDockerPullCalled:  false,
			expectDockerTagCalled:   false,
			expectDockerPushCalled:  false,
			expectedRemoteImage:     "nginx",
			expectError:             false,
		},
		{
			name:                    "Source image with existing package path and registry",
			registry:                osutil.NewExpandableString("contoso.azurecr.io"),
			packagePath:             "my-project/my-service:azd-deploy-0",
			expectedRemoteImage:     "contoso.azurecr.io/my-project/my-service:azd-deploy-0",
			expectDockerLoginCalled: true,
			expectDockerPullCalled:  false,
			expectDockerTagCalled:   true,
			expectDockerPushCalled:  true,
			expectError:             false,
		},
		{
			name:                    "Empty package details",
			dockerDetails:           &dockerPackageResult{},
			expectError:             true,
			expectDockerLoginCalled: false,
			expectDockerPullCalled:  false,
			expectDockerTagCalled:   false,
			expectDockerPushCalled:  false,
		},
		{
			name:                    "Nil package details",
			dockerDetails:           nil,
			expectError:             true,
			expectDockerLoginCalled: false,
			expectDockerPullCalled:  false,
			expectDockerTagCalled:   false,
			expectDockerPushCalled:  false,
		},
	}

	targetResource := environment.NewTargetResource(
		"SUBSCRIPTION_ID",
		"RESOURCE_GROUP",
		"CONTAINER_APP",
		"Microsoft.App/containerApps",
	)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(context.Background())
			mockResults := setupDockerMocks(mockContext)
			env := environment.NewWithValues("dev", map[string]string{})
			dockerCli := docker.NewCli(mockContext.CommandRunner)
			dotnetCli := dotnet.NewCli(mockContext.CommandRunner)
			envManager := &mockenv.MockEnvManager{}
			envManager.On("Save", *mockContext.Context, env).Return(nil)

			mockContainerRegistryService := &mockContainerRegistryService{}
			setupContainerRegistryMocks(mockContext, &mockContainerRegistryService.Mock)

			containerHelper := NewContainerHelper(
				env,
				envManager,
				clock.NewMock(),
				mockContainerRegistryService,
				nil,
				dockerCli,
				dotnetCli,
				mockContext.Console,
				cloud.AzurePublic(),
			)
			serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)

			serviceConfig.Image = osutil.NewExpandableString(tt.image)
			serviceConfig.RelativePath = tt.project
			serviceConfig.Docker.Registry = tt.registry

			packageOutput := &ServicePackageResult{
				Details:     tt.dockerDetails,
				PackagePath: tt.packagePath,
			}

			deployResult, err := logProgress(
				t, func(progress *async.Progress[ServiceProgress]) (*ServiceDeployResult, error) {
					return containerHelper.Deploy(
						*mockContext.Context, serviceConfig, packageOutput, targetResource, true, progress)
				},
			)

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Same(t, packageOutput, deployResult.Package)

				if deployResult.Details != nil {
					dockerDeployResult, ok := deployResult.Details.(*dockerDeployResult)
					require.True(t, ok)
					require.Equal(t, tt.expectedRemoteImage, dockerDeployResult.RemoteImageTag)
				}
			}

			_, dockerPullCalled := mockResults["docker-pull"]
			_, dockerTagCalled := mockResults["docker-tag"]
			_, dockerPushCalled := mockResults["docker-push"]

			if tt.expectDockerLoginCalled {
				registryName, err := tt.registry.Envsubst(env.Getenv)
				require.NoError(t, err)

				mockContainerRegistryService.AssertCalled(
					t,
					"Login",
					*mockContext.Context,
					env.GetSubscriptionId(),
					registryName,
				)
			} else {
				mockContainerRegistryService.AssertNotCalled(t, "Login")
			}

			require.Equal(t, tt.expectDockerPullCalled, dockerPullCalled)
			require.Equal(t, tt.expectDockerTagCalled, dockerTagCalled)
			require.Equal(t, tt.expectDockerPushCalled, dockerPushCalled)
			require.Equal(t, tt.expectedRemoteImage, env.GetServiceProperty("api", "IMAGE_NAME"))
		})
	}
}

func Test_ContainerHelper_ConfiguredImage(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	env := environment.NewWithValues("dev", map[string]string{})
	containerHelper := NewContainerHelper(env, nil, clock.NewMock(), nil, nil, nil, nil, nil, cloud.AzurePublic())

	tests := []struct {
		name                 string
		projectName          string
		serviceName          string
		sourceImage          string
		env                  map[string]string
		registry             osutil.ExpandableString
		image                osutil.ExpandableString
		tag                  osutil.ExpandableString
		expectedImage        docker.ContainerImage
		expectError          bool
		expectedErrorMessage string
	}{
		{
			name: "with defaults",
			expectedImage: docker.ContainerImage{
				Registry:   "",
				Repository: "test-app/api-dev",
				Tag:        "azd-deploy-0",
			},
		},
		{
			name: "with custom tag",
			tag:  osutil.NewExpandableString("custom-tag"),
			expectedImage: docker.ContainerImage{
				Registry:   "",
				Repository: "test-app/api-dev",
				Tag:        "custom-tag",
			},
		},
		{
			name:  "with custom image",
			image: osutil.NewExpandableString("custom-image"),
			expectedImage: docker.ContainerImage{
				Registry:   "",
				Repository: "custom-image",
				Tag:        "azd-deploy-0",
			},
		},
		{
			name:  "with custom image and tag",
			image: osutil.NewExpandableString("custom-image"),
			tag:   osutil.NewExpandableString("custom-tag"),
			expectedImage: docker.ContainerImage{
				Registry:   "",
				Repository: "custom-image",
				Tag:        "custom-tag",
			},
		},
		{
			name:     "with registry",
			registry: osutil.NewExpandableString("contoso.azurecr.io"),
			expectedImage: docker.ContainerImage{
				Registry:   "contoso.azurecr.io",
				Repository: "test-app/api-dev",
				Tag:        "azd-deploy-0",
			},
		},
		{
			name:     "with registry, custom image and tag",
			registry: osutil.NewExpandableString("contoso.azurecr.io"),
			image:    osutil.NewExpandableString("custom-image"),
			tag:      osutil.NewExpandableString("custom-tag"),
			expectedImage: docker.ContainerImage{
				Registry:   "contoso.azurecr.io",
				Repository: "custom-image",
				Tag:        "custom-tag",
			},
		},
		{
			name: "with expandable overrides",
			env: map[string]string{
				"MY_CUSTOM_REGISTRY": "custom.azurecr.io",
				"MY_CUSTOM_IMAGE":    "custom-image",
				"MY_CUSTOM_TAG":      "custom-tag",
			},
			registry: osutil.NewExpandableString("${MY_CUSTOM_REGISTRY}"),
			image:    osutil.NewExpandableString("${MY_CUSTOM_IMAGE}"),
			tag:      osutil.NewExpandableString("${MY_CUSTOM_TAG}"),
			expectedImage: docker.ContainerImage{
				Registry:   "custom.azurecr.io",
				Repository: "custom-image",
				Tag:        "custom-tag",
			},
		},
		{
			name:                 "invalid image name",
			image:                osutil.NewExpandableString("${MISSING_CLOSING_BRACE"),
			expectError:          true,
			expectedErrorMessage: "missing closing brace",
		},
		{
			name:                 "invalid tag",
			image:                osutil.NewExpandableString("repo/::latest"),
			expectError:          true,
			expectedErrorMessage: "invalid tag",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)
			if tt.projectName != "" {
				serviceConfig.Project.Name = tt.projectName
			}
			if tt.serviceName != "" {
				serviceConfig.Name = tt.serviceName
			}
			serviceConfig.Image = osutil.NewExpandableString(tt.sourceImage)
			serviceConfig.Docker.Registry = tt.registry
			serviceConfig.Docker.Image = tt.image
			serviceConfig.Docker.Tag = tt.tag

			for k, v := range tt.env {
				env.DotenvSet(k, v)
			}

			image, err := containerHelper.GeneratedImage(*mockContext.Context, serviceConfig)

			if tt.expectError {
				require.Error(t, err)
				require.Nil(t, image)
				if tt.expectedErrorMessage != "" {
					require.Contains(t, err.Error(), tt.expectedErrorMessage)
				}
			} else {
				require.NoError(t, err)
				require.NotNil(t, image)
				require.Equal(t, tt.expectedImage, *image)
			}
		})
	}
}

type mockContainerRegistryServiceForRetry struct {
	MaxRetry   int
	retryCount int
	mock.Mock
}

func (m *mockContainerRegistryServiceForRetry) totalRetries() int {
	return m.retryCount
}

func (m *mockContainerRegistryServiceForRetry) Login(ctx context.Context, subscriptionId string, loginServer string) error {
	args := m.Called(ctx, subscriptionId, loginServer)
	return args.Error(0)
}

func (m *mockContainerRegistryServiceForRetry) Credentials(
	ctx context.Context,
	subscriptionId string,
	loginServer string,
) (*azapi.DockerCredentials, error) {
	if m.retryCount < m.MaxRetry {
		m.retryCount++
		return nil, &azcore.ResponseError{
			StatusCode: http.StatusNotFound,
		}
	}
	return &azapi.DockerCredentials{}, nil
}

func (m *mockContainerRegistryServiceForRetry) GetContainerRegistries(
	ctx context.Context,
	subscriptionId string,
) ([]*armcontainerregistry.Registry, error) {
	args := m.Called(ctx, subscriptionId)
	return args.Get(0).([]*armcontainerregistry.Registry), args.Error(1)
}

func Test_ContainerHelper_Credential_Retry(t *testing.T) {
	t.Run("Retry on 404 on time", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		env := environment.New("dev")
		envManager := &mockenv.MockEnvManager{}

		mockContainerService := &mockContainerRegistryServiceForRetry{
			MaxRetry: 1,
		}
		// no need to delay in tests
		defaultCredentialsRetryDelay = 1 * time.Millisecond

		containerHelper := NewContainerHelper(
			env, envManager, clock.NewMock(), mockContainerService, nil, nil, nil, nil, cloud.AzurePublic())

		serviceConfig := createTestServiceConfig("path", ContainerAppTarget, ServiceLanguageDotNet)
		serviceConfig.Docker.Registry = osutil.NewExpandableString("contoso.azurecr.io")
		targetResource := environment.NewTargetResource("sub", "rg", "name", "rType")

		credential, err := containerHelper.Credentials(*mockContext.Context, serviceConfig, targetResource)
		require.NoError(t, err)
		require.NotNil(t, credential)
		require.Equal(t, 1, mockContainerService.totalRetries())
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

		if args.Args[1] == "" || args.Args[2] == "" {
			return exec.NewRunResult(1, "", ""), errors.New("no image or tag")
		}

		return exec.NewRunResult(0, "", ""), nil
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "docker push")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		mockResults["docker-push"] = args

		if args.Args[1] == "" {
			return exec.NewRunResult(1, "", ""), errors.New("no image")
		}

		return exec.NewRunResult(0, "", ""), nil
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "docker pull")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		mockResults["docker-pull"] = args

		if args.Args[1] == "" {
			return exec.NewRunResult(1, "", ""), errors.New("no image")
		}

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
) (*azapi.DockerCredentials, error) {
	args := m.Called(ctx, subscriptionId, loginServer)
	return args.Get(0).(*azapi.DockerCredentials), args.Error(1)
}

func (m *mockContainerRegistryService) GetContainerRegistries(
	ctx context.Context,
	subscriptionId string,
) ([]*armcontainerregistry.Registry, error) {
	args := m.Called(ctx, subscriptionId)
	return args.Get(0).([]*armcontainerregistry.Registry), args.Error(1)
}
