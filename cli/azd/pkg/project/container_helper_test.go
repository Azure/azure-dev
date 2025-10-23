// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

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
			containerHelper := NewContainerHelper(
				env, nil, clock.NewMock(), nil, nil, mockContext.CommandRunner,
				nil, nil, nil, cloud.AzurePublic())
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
	containerHelper := NewContainerHelper(
		env, nil, clock.NewMock(), nil, nil, mockContext.CommandRunner,
		nil, nil, nil, cloud.AzurePublic())

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serviceConfig := createTestServiceConfig(tt.project, ContainerAppTarget, ServiceLanguageTypeScript)
			serviceConfig.Docker.Registry = tt.registry

			remoteTag, err := containerHelper.RemoteImageTag(*mockContext.Context, serviceConfig, tt.localImageTag, nil)

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

func Test_parsePublishOptionsToImageOverride(t *testing.T) {
	tests := []struct {
		name           string
		options        *PublishOptions
		expectedResult *imageOverride
		expectedError  bool
		errorContains  string
	}{
		{
			name:           "nil options",
			options:        nil,
			expectedResult: nil,
			expectedError:  false,
		},
		{
			name:           "empty image",
			options:        &PublishOptions{Image: ""},
			expectedResult: nil,
			expectedError:  false,
		},
		{
			name:    "repository only",
			options: &PublishOptions{Image: "myapp/api"},
			expectedResult: &imageOverride{
				Registry:   "",
				Repository: "myapp/api",
				Tag:        "",
			},
			expectedError: false,
		},
		{
			name:    "repository with tag",
			options: &PublishOptions{Image: "myapp/api:v1.0.0"},
			expectedResult: &imageOverride{
				Registry:   "",
				Repository: "myapp/api",
				Tag:        "v1.0.0",
			},
			expectedError: false,
		},
		{
			name:    "registry with repository",
			options: &PublishOptions{Image: "contoso.azurecr.io/myapp/api"},
			expectedResult: &imageOverride{
				Registry:   "contoso.azurecr.io",
				Repository: "myapp/api",
				Tag:        "",
			},
			expectedError: false,
		},
		{
			name:    "registry with repository and tag",
			options: &PublishOptions{Image: "contoso.azurecr.io/myapp/api:v1.0.0"},
			expectedResult: &imageOverride{
				Registry:   "contoso.azurecr.io",
				Repository: "myapp/api",
				Tag:        "v1.0.0",
			},
			expectedError: false,
		},
		{
			name:    "dockerhub with org and repo",
			options: &PublishOptions{Image: "docker.io/myorg/myapp:latest"},
			expectedResult: &imageOverride{
				Registry:   "docker.io",
				Repository: "myorg/myapp",
				Tag:        "latest",
			},
			expectedError: false,
		},
		{
			name:    "simple image name with tag",
			options: &PublishOptions{Image: "nginx:latest"},
			expectedResult: &imageOverride{
				Registry:   "",
				Repository: "nginx",
				Tag:        "latest",
			},
			expectedError: false,
		},
		{
			name:           "invalid image format - too many colons",
			options:        &PublishOptions{Image: "image:tag:extra"},
			expectedResult: nil,
			expectedError:  true,
			errorContains:  "invalid image format",
		},
		{
			name:           "empty repository",
			options:        &PublishOptions{Image: ":tag"},
			expectedResult: nil,
			expectedError:  true,
			errorContains:  "invalid image format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseImageOverride(tt.options)

			if tt.expectedError {
				require.Error(t, err)
				if tt.errorContains != "" {
					require.Contains(t, err.Error(), tt.errorContains)
				}
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				if tt.expectedResult == nil {
					require.Nil(t, result)
				} else {
					require.NotNil(t, result)
					require.Equal(t, tt.expectedResult.Registry, result.Registry)
					require.Equal(t, tt.expectedResult.Repository, result.Repository)
					require.Equal(t, tt.expectedResult.Tag, result.Tag)
				}
			}
		})
	}
}

func Test_ContainerHelper_RemoteImageTag_WithImageOverride(t *testing.T) {
	tests := []struct {
		name              string
		project           string
		localImageTag     string
		registry          osutil.ExpandableString
		imageOverride     *imageOverride
		expectedRemoteTag string
		expectError       bool
	}{
		{
			name:          "with override repository only",
			project:       "./src/api",
			registry:      osutil.NewExpandableString("contoso.azurecr.io"),
			localImageTag: "test-app/api-dev:azd-deploy-0",
			imageOverride: &imageOverride{
				Repository: "custom/image",
			},
			expectedRemoteTag: "contoso.azurecr.io/custom/image:azd-deploy-0",
		},
		{
			name:          "with override tag only",
			project:       "./src/api",
			registry:      osutil.NewExpandableString("contoso.azurecr.io"),
			localImageTag: "test-app/api-dev:azd-deploy-0",
			imageOverride: &imageOverride{
				Tag: "latest",
			},
			expectedRemoteTag: "contoso.azurecr.io/test-app/api-dev:latest",
		},
		{
			name:          "with both override repository and tag",
			project:       "./src/api",
			registry:      osutil.NewExpandableString("contoso.azurecr.io"),
			localImageTag: "test-app/api-dev:azd-deploy-0",
			imageOverride: &imageOverride{
				Repository: "custom/image",
				Tag:        "latest",
			},
			expectedRemoteTag: "contoso.azurecr.io/custom/image:latest",
		},
		{
			name:          "with override registry only",
			project:       "./src/api",
			registry:      osutil.NewExpandableString("contoso.azurecr.io"),
			localImageTag: "test-app/api-dev:azd-deploy-0",
			imageOverride: &imageOverride{
				Registry: "docker.io",
			},
			expectedRemoteTag: "docker.io/test-app/api-dev:azd-deploy-0",
		},
		{
			name:          "with all overrides",
			project:       "./src/api",
			registry:      osutil.NewExpandableString("contoso.azurecr.io"),
			localImageTag: "test-app/api-dev:azd-deploy-0",
			imageOverride: &imageOverride{
				Registry:   "docker.io",
				Repository: "myorg/myimage",
				Tag:        "v2.0.0",
			},
			expectedRemoteTag: "docker.io/myorg/myimage:v2.0.0",
		},
		{
			name:          "repository override with no slash prefix",
			project:       "./src/api",
			registry:      osutil.NewExpandableString("contoso.azurecr.io"),
			localImageTag: "test-app/api-dev:azd-deploy-0",
			imageOverride: &imageOverride{
				Repository: "myimage",
			},
			expectedRemoteTag: "contoso.azurecr.io/myimage:azd-deploy-0",
		},
		{
			name:              "no override - nil",
			project:           "./src/api",
			registry:          osutil.NewExpandableString("contoso.azurecr.io"),
			localImageTag:     "test-app/api-dev:azd-deploy-0",
			imageOverride:     nil,
			expectedRemoteTag: "contoso.azurecr.io/test-app/api-dev:azd-deploy-0",
		},
		{
			name:              "no override - empty",
			project:           "./src/api",
			registry:          osutil.NewExpandableString("contoso.azurecr.io"),
			localImageTag:     "test-app/api-dev:azd-deploy-0",
			imageOverride:     &imageOverride{},
			expectedRemoteTag: "contoso.azurecr.io/test-app/api-dev:azd-deploy-0",
		},
	}

	mockContext := mocks.NewMockContext(context.Background())
	env := environment.NewWithValues("dev", map[string]string{})
	containerHelper := NewContainerHelper(
		env, nil, clock.NewMock(), nil, nil, mockContext.CommandRunner,
		nil, nil, nil, cloud.AzurePublic())

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serviceConfig := createTestServiceConfig(tt.project, ContainerAppTarget, ServiceLanguageTypeScript)
			serviceConfig.Docker.Registry = tt.registry

			remoteTag, err := containerHelper.RemoteImageTag(
				*mockContext.Context, serviceConfig, tt.localImageTag, tt.imageOverride)

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
		containerHelper := NewContainerHelper(
			env, envManager, clock.NewMock(), nil, nil, nil,
			nil, nil, nil, cloud.AzurePublic())
		serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)
		registryName, err := containerHelper.RegistryName(*mockContext.Context, serviceConfig)

		require.NoError(t, err)
		require.Equal(t, "contoso.azurecr.io", registryName)
	})

	t.Run("Azure Yaml with simple string", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		env := environment.NewWithValues("dev", map[string]string{})
		envManager := &mockenv.MockEnvManager{}
		containerHelper := NewContainerHelper(
			env, envManager, clock.NewMock(), nil, nil, nil,
			nil, nil, nil, cloud.AzurePublic())
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
		containerHelper := NewContainerHelper(
			env, envManager, clock.NewMock(), nil, nil, nil,
			nil, nil, nil, cloud.AzurePublic())
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
		containerHelper := NewContainerHelper(
			env, envManager, clock.NewMock(), nil, nil, nil,
			nil, nil, nil, cloud.AzurePublic())
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
		dockerArtifact          *Artifact
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
			dockerArtifact: &Artifact{
				Kind:     ArtifactKindContainer,
				Location: "my-project/my-service:azd-deploy-0",
				Metadata: map[string]string{
					"imageHash":   "IMAGE_ID",
					"sourceImage": "",
					"targetImage": "my-project/my-service:azd-deploy-0",
				},
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
			dockerArtifact: &Artifact{
				Kind:     ArtifactKindContainer,
				Location: "my-project/my-service:azd-deploy-0",
				Metadata: map[string]string{
					"imageHash":   "IMAGE_ID",
					"sourceImage": "",
					"targetImage": "my-project/my-service:azd-deploy-0",
				},
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
			dockerArtifact: &Artifact{
				Kind:     ArtifactKindContainer,
				Location: "my-project/nginx:azd-deploy-0",
				Metadata: map[string]string{
					"imageHash":   "",
					"sourceImage": "nginx",
					"targetImage": "my-project/nginx:azd-deploy-0",
				},
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
			dockerArtifact: &Artifact{
				Kind:     ArtifactKindContainer,
				Location: "my-project/nginx:azd-deploy-0",
				Metadata: map[string]string{
					"imageHash":   "",
					"sourceImage": "nginx",
					"targetImage": "my-project/nginx:azd-deploy-0",
				},
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
			dockerArtifact: &Artifact{
				Kind:     ArtifactKindContainer,
				Location: "my-project/nginx:azd-deploy-0",
				Metadata: map[string]string{
					"imageHash":   "",
					"sourceImage": "nginx",
					"targetImage": "my-project/nginx:azd-deploy-0",
				},
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
			dockerArtifact:          &Artifact{Kind: ArtifactKindContainer, Location: "", Metadata: map[string]string{}},
			expectError:             true,
			expectDockerLoginCalled: false,
			expectDockerPullCalled:  false,
			expectDockerTagCalled:   false,
			expectDockerPushCalled:  false,
		},
		{
			name:                    "Nil package details",
			dockerArtifact:          nil,
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
				mockContext.CommandRunner,
				dockerCli,
				dotnetCli,
				mockContext.Console,
				cloud.AzurePublic(),
			)
			serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)

			serviceConfig.Image = osutil.NewExpandableString(tt.image)
			serviceConfig.RelativePath = tt.project
			serviceConfig.Docker.Registry = tt.registry

			var packageArtifacts ArtifactCollection
			if tt.dockerArtifact != nil {
				packageArtifacts = ArtifactCollection{tt.dockerArtifact}
			} else if tt.packagePath != "" {
				packageArtifacts = ArtifactCollection{
					{
						Kind:         ArtifactKindContainer,
						Location:     tt.packagePath,
						LocationKind: LocationKindLocal,
						Metadata:     map[string]string{},
					},
				}
			}

			packageOutput := &ServicePackageResult{
				Artifacts: packageArtifacts,
			}

			serviceContext := &ServiceContext{
				Package: packageOutput.Artifacts,
			}

			publishResult, err := logProgress(
				t, func(progress *async.Progress[ServiceProgress]) (*ServicePublishResult, error) {
					return containerHelper.Publish(
						*mockContext.Context, serviceConfig, serviceContext, targetResource, progress, &PublishOptions{})
				},
			)

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Len(t, publishResult.Artifacts, 1)
				artifact := publishResult.Artifacts[0]
				require.Equal(t, ArtifactKindContainer, artifact.Kind)
				require.Equal(t, tt.expectedRemoteImage, artifact.Metadata["remoteImage"])
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

			if !tt.expectError {
				require.Len(t, publishResult.Artifacts, 1)
				artifact := publishResult.Artifacts[0]
				require.Equal(t, ArtifactKindContainer, artifact.Kind)
				require.Equal(t, tt.expectedRemoteImage, artifact.Metadata["remoteImage"])
			}
		})
	}
}

func Test_ContainerHelper_ConfiguredImage(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	env := environment.NewWithValues("dev", map[string]string{})
	containerHelper := NewContainerHelper(
		env, nil, clock.NewMock(), nil, nil, mockContext.CommandRunner,
		nil, nil, nil, cloud.AzurePublic())

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
			env, envManager, clock.NewMock(), mockContainerService, nil, nil, nil, nil, nil, cloud.AzurePublic())

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

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "docker manifest inspect")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		mockResults["docker-manifest-inspect"] = args

		if len(args.Args) < 4 || args.Args[3] == "" {
			return exec.NewRunResult(1, "", ""), errors.New("no image specified")
		}

		// For the test, we'll assume the image doesn't exist (return exit code 1)
		// This simulates the normal case where the image needs to be built and pushed
		return exec.NewRunResult(1, "", "manifest unknown"), nil
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
func Test_ContainerHelper_Publish(t *testing.T) {
	tests := []struct {
		name                    string
		registry                osutil.ExpandableString
		image                   string
		project                 string
		packagePath             string
		imageHash               string
		sourceImage             string
		targetImage             string
		publishOptions          *PublishOptions
		expectedRemoteImage     string
		expectDockerLoginCalled bool
		expectDockerPullCalled  bool
		expectDockerTagCalled   bool
		expectDockerPushCalled  bool
		expectError             bool
	}{
		{
			name:                    "Source code and registry",
			project:                 "./src/api",
			registry:                osutil.NewExpandableString("contoso.azurecr.io"),
			packagePath:             "my-project/my-service:azd-publish-0",
			imageHash:               "IMAGE_ID",
			sourceImage:             "",
			targetImage:             "my-project/my-service:azd-publish-0",
			publishOptions:          &PublishOptions{},
			expectDockerLoginCalled: true,
			expectDockerPullCalled:  false,
			expectDockerTagCalled:   true,
			expectDockerPushCalled:  true,
			expectedRemoteImage:     "contoso.azurecr.io/my-project/my-service:azd-publish-0",
			expectError:             false,
		},
		{
			name:                    "Source code and no registry",
			project:                 "./src/api",
			packagePath:             "my-project/my-service:azd-publish-0",
			imageHash:               "IMAGE_ID",
			sourceImage:             "",
			targetImage:             "my-project/my-service:azd-publish-0",
			publishOptions:          &PublishOptions{},
			expectError:             true,
			expectDockerLoginCalled: false,
			expectDockerPullCalled:  false,
			expectDockerTagCalled:   false,
			expectDockerPushCalled:  false,
		},
		{
			name:                    "Source image and registry",
			image:                   "nginx",
			registry:                osutil.NewExpandableString("contoso.azurecr.io"),
			packagePath:             "my-project/nginx:azd-publish-0",
			imageHash:               "",
			sourceImage:             "nginx",
			targetImage:             "my-project/nginx:azd-publish-0",
			publishOptions:          &PublishOptions{},
			expectDockerLoginCalled: true,
			expectDockerPullCalled:  true,
			expectDockerTagCalled:   true,
			expectDockerPushCalled:  true,
			expectedRemoteImage:     "contoso.azurecr.io/my-project/nginx:azd-publish-0",
			expectError:             false,
		},
		{
			name:                    "Source image and no registry",
			image:                   "nginx",
			packagePath:             "my-project/nginx:azd-publish-0",
			imageHash:               "",
			sourceImage:             "nginx",
			targetImage:             "my-project/nginx:azd-publish-0",
			publishOptions:          &PublishOptions{},
			expectDockerLoginCalled: false,
			expectDockerPullCalled:  false,
			expectDockerTagCalled:   false,
			expectDockerPushCalled:  false,
			expectedRemoteImage:     "nginx",
			expectError:             false,
		},
		{
			name:                    "With publish options overwrite",
			project:                 "./src/api",
			registry:                osutil.NewExpandableString("contoso.azurecr.io"),
			imageHash:               "IMAGE_ID",
			sourceImage:             "",
			targetImage:             "my-project/my-service:azd-publish-0",
			publishOptions:          nil,
			expectDockerLoginCalled: true,
			expectDockerPullCalled:  false,
			expectDockerTagCalled:   true,
			expectDockerPushCalled:  true,
			expectedRemoteImage:     "contoso.azurecr.io/my-project/my-service:azd-publish-0",
			expectError:             false,
		},
		{
			name:                    "With publish options image override",
			project:                 "./src/api",
			registry:                osutil.NewExpandableString("contoso.azurecr.io"),
			packagePath:             "my-project/my-service:azd-publish-0",
			imageHash:               "IMAGE_ID",
			sourceImage:             "",
			targetImage:             "my-project/my-service:azd-publish-0",
			publishOptions:          &PublishOptions{Image: "myregistry.azurecr.io/myapp/myservice:v2.0.0"},
			expectDockerLoginCalled: true,
			expectDockerPullCalled:  false,
			expectDockerTagCalled:   true,
			expectDockerPushCalled:  true,
			expectedRemoteImage:     "myregistry.azurecr.io/myapp/myservice:v2.0.0",
			expectError:             false,
		},
		{
			name:                    "Empty package details",
			packagePath:             "",
			imageHash:               "",
			sourceImage:             "",
			targetImage:             "",
			publishOptions:          &PublishOptions{},
			expectError:             true,
			expectDockerLoginCalled: false,
			expectDockerPullCalled:  false,
			expectDockerTagCalled:   false,
			expectDockerPushCalled:  false,
		},
		{
			name:                    "Nil package details",
			packagePath:             "",
			imageHash:               "",
			sourceImage:             "",
			targetImage:             "",
			publishOptions:          &PublishOptions{},
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
				mockContext.CommandRunner,
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
				Artifacts: ArtifactCollection{
					{
						Kind:         ArtifactKindContainer,
						Location:     tt.packagePath,
						LocationKind: LocationKindLocal,
						Metadata: map[string]string{
							"imageHash":   tt.imageHash,
							"sourceImage": tt.sourceImage,
							"targetImage": tt.targetImage,
						},
					},
				},
			}

			serviceContext := &ServiceContext{
				Package: packageOutput.Artifacts,
			}

			publishResult, err := logProgress(
				t, func(progress *async.Progress[ServiceProgress]) (*ServicePublishResult, error) {
					return containerHelper.Publish(
						*mockContext.Context, serviceConfig, serviceContext, targetResource, progress, tt.publishOptions)
				},
			)

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.NotNil(t, publishResult)
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

			if !tt.expectError {
				require.Len(t, publishResult.Artifacts, 1)
				artifact := publishResult.Artifacts[0]
				require.Equal(t, ArtifactKindContainer, artifact.Kind)
				require.Equal(t, tt.expectedRemoteImage, artifact.Metadata["remoteImage"])
			}
		})
	}
}
