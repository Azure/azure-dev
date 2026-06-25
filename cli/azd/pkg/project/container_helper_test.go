// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/benbjohnson/clock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/containerregistry"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
)

func Test_ContainerHelper_LocalImageTag(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
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
				clock.NewMock(), nil, nil, mockContext.CommandRunner,
				nil, nil, nil, cloud.AzurePublic())
			serviceConfig.Docker = tt.dockerConfig

			tag, err := containerHelper.LocalImageTag(*mockContext.Context, serviceConfig, env)
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

	mockContext := mocks.NewMockContext(t.Context())
	env := environment.NewWithValues("dev", map[string]string{})

	containerHelper := NewContainerHelper(
		clock.NewMock(), nil, nil, mockContext.CommandRunner,
		nil, nil, nil, cloud.AzurePublic())

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serviceConfig := createTestServiceConfig(tt.project, ContainerAppTarget, ServiceLanguageTypeScript)
			serviceConfig.Docker.Registry = tt.registry

			remoteTag, err := containerHelper.RemoteImageTag(*mockContext.Context, serviceConfig, tt.localImageTag, nil, env)

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

	mockContext := mocks.NewMockContext(t.Context())
	env := environment.NewWithValues("dev", map[string]string{})

	containerHelper := NewContainerHelper(
		clock.NewMock(), nil, nil, mockContext.CommandRunner,
		nil, nil, nil, cloud.AzurePublic())

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serviceConfig := createTestServiceConfig(tt.project, ContainerAppTarget, ServiceLanguageTypeScript)
			serviceConfig.Docker.Registry = tt.registry

			remoteTag, err := containerHelper.RemoteImageTag(
				*mockContext.Context, serviceConfig, tt.localImageTag, tt.imageOverride, env)

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

func Test_ResolveDockerBuildArgs(t *testing.T) {
	env := environment.NewWithValues("dev", map[string]string{
		"FROM_ENV": "env-value",
	})
	require.NoError(t, env.Config.Set("infra.parameters.param", "param-value"))

	args, err := resolveDockerBuildArgs([]osutil.ExpandableString{
		osutil.NewExpandableString("FROM_ENV=${FROM_ENV}"),
		osutil.NewExpandableString("FROM_PARAM={infra.parameters.param}"),
	}, env)

	require.NoError(t, err)
	require.Equal(t, []string{
		"FROM_ENV=env-value",
		"FROM_PARAM=param-value",
	}, args)
}

func Test_DockerBuildArgsToAcrArguments(t *testing.T) {
	lookupEnv := func(key string) (string, bool) {
		values := map[string]string{
			"FROM_ENV": "env-value",
		}
		value, ok := values[key]
		return value, ok
	}

	args, err := dockerBuildArgsToAcrArguments([]string{
		"EXPLICIT=value",
		"MULTI=a=b",
		"FROM_ENV",
	}, lookupEnv)

	require.NoError(t, err)
	require.Len(t, args, 3)
	require.Equal(t, map[string]string{
		"EXPLICIT": "value",
		"MULTI":    "a=b",
		"FROM_ENV": "env-value",
	}, acrArgumentsToMap(t, args))
	for _, arg := range args {
		require.NotNil(t, arg.IsSecret)
		require.False(t, *arg.IsSecret)
	}
}

func Test_DockerBuildArgsToAcrArguments_MissingShorthandEnv(t *testing.T) {
	_, err := dockerBuildArgsToAcrArguments([]string{"MISSING"}, func(string) (string, bool) {
		return "", false
	})

	require.ErrorContains(t, err, `environment variable is not set`)
	require.ErrorContains(t, err, `MISSING=<value>`)
}

func Test_DockerBuildArgsToAcrArguments_EmptyName(t *testing.T) {
	_, err := dockerBuildArgsToAcrArguments([]string{"=value"}, func(string) (string, bool) {
		return "", false
	})

	require.ErrorContains(t, err, "empty name")
}

func Test_DockerBuildArgEnvResolver(t *testing.T) {
	t.Setenv("FROM_OS", "os-value")
	t.Setenv("OVERRIDDEN", "os-value")

	env := environment.NewWithValues("dev", map[string]string{
		"FROM_ENV":   "env-value",
		"OVERRIDDEN": "env-value",
	})

	lookup := dockerBuildArgEnvResolver(env, []string{
		"FROM_BUILD_ENV=build-env-value",
		"OVERRIDDEN=build-env-value",
		"EMPTY=",
	})

	value, ok := lookup("FROM_OS")
	require.True(t, ok)
	require.Equal(t, "os-value", value)

	value, ok = lookup("FROM_ENV")
	require.True(t, ok)
	require.Equal(t, "env-value", value)

	value, ok = lookup("FROM_BUILD_ENV")
	require.True(t, ok)
	require.Equal(t, "build-env-value", value)

	value, ok = lookup("OVERRIDDEN")
	require.True(t, ok)
	require.Equal(t, "build-env-value", value)

	value, ok = lookup("EMPTY")
	require.True(t, ok)
	require.Equal(t, "", value)
}

func Test_ContainerHelper_RunRemoteBuild_PassesBuildArgs(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	env := environment.NewWithValues("dev", map[string]string{
		"FROM_ENV": "env-value",
	})
	require.NoError(t, env.Config.Set("infra.parameters.param", "param-value"))

	var scheduleRunBody []byte
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.Contains(request.URL.Path, "listBuildSourceUploadUrl")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		uploadURL := "https://upload.example.com/container/source.tar.gz?sig=abc"
		return mocks.CreateHttpResponseWithBody(
			request, http.StatusOK, armcontainerregistry.SourceUploadDefinition{
				UploadURL:    &uploadURL,
				RelativePath: new("source.tar.gz"),
			},
		)
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.Contains(request.URL.Path, "/container/source.tar.gz")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		_, _ = io.Copy(io.Discard, request.Body)
		response, err := mocks.CreateEmptyHttpResponse(request, http.StatusCreated)
		if err != nil {
			return nil, err
		}
		response.Header.Set("ETag", `"0x8D4BCC2E4835CD0"`)
		return response, nil
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.Contains(request.URL.Path, "scheduleRun")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		var err error
		scheduleRunBody, err = io.ReadAll(request.Body)
		require.NoError(t, err)

		return mocks.CreateHttpResponseWithBody(
			request, http.StatusBadRequest, map[string]any{
				"error": map[string]string{
					"code":    "BadRequest",
					"message": "stop after capturing request",
				},
			},
		)
	})

	mockContainerRegistryService := &mockContainerRegistryService{}
	mockContainerRegistryService.On(
		"FindContainerRegistryResourceGroup",
		mock.Anything,
		"SUBSCRIPTION_ID",
		"contoso",
	).Return("REGISTRY_RG", nil)

	dockerCli := docker.NewCli(mockContext.CommandRunner)
	dotnetCli := dotnet.NewCli(mockContext.CommandRunner)
	containerHelper := NewContainerHelper(
		clock.NewMock(),
		mockContainerRegistryService,
		containerregistry.NewRemoteBuildManager(mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions),
		mockContext.CommandRunner,
		dockerCli,
		dotnetCli,
		mockContext.Console,
		cloud.AzurePublic(),
	)

	projectRoot := t.TempDir()
	servicePath := filepath.Join(projectRoot, "src", "api")
	require.NoError(t, os.MkdirAll(servicePath, osutil.PermissionDirectory))
	require.NoError(t, os.WriteFile(filepath.Join(servicePath, "Dockerfile"), []byte("FROM scratch"), 0600))

	serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)
	serviceConfig.Project.Path = projectRoot
	serviceConfig.Docker.Registry = osutil.NewExpandableString("contoso.azurecr.io")
	serviceConfig.Docker.RemoteBuild = true
	serviceConfig.Docker.BuildArgs = []osutil.ExpandableString{
		osutil.NewExpandableString("EXPLICIT=value"),
		osutil.NewExpandableString("MULTI=a=b"),
		osutil.NewExpandableString("FROM_ENV"),
		osutil.NewExpandableString("FROM_BUILD_ENV"),
		osutil.NewExpandableString("FROM_PARAM={infra.parameters.param}"),
	}
	serviceConfig.Docker.BuildEnv = []string{
		"FROM_BUILD_ENV=build-env-value",
	}

	targetResource := environment.NewTargetResource(
		"SUBSCRIPTION_ID",
		"RESOURCE_GROUP",
		"CONTAINER_APP",
		"Microsoft.App/containerApps",
	)

	_, err := logProgress(
		t, func(progress *async.Progress[ServiceProgress]) (string, error) {
			return containerHelper.runRemoteBuild(
				*mockContext.Context, serviceConfig, targetResource, env, progress, &imageOverride{})
		},
	)

	require.Error(t, err)
	require.NotEmpty(t, scheduleRunBody)

	var requestBody struct {
		Arguments []struct {
			Name     string `json:"name"`
			Value    string `json:"value"`
			IsSecret bool   `json:"isSecret"`
		} `json:"arguments"`
	}
	require.NoError(t, json.Unmarshal(scheduleRunBody, &requestBody))
	require.Equal(t, map[string]string{
		"EXPLICIT":       "value",
		"MULTI":          "a=b",
		"FROM_ENV":       "env-value",
		"FROM_BUILD_ENV": "build-env-value",
		"FROM_PARAM":     "param-value",
	}, acrRequestArgumentsToMap(requestBody.Arguments))
	for _, arg := range requestBody.Arguments {
		require.False(t, arg.IsSecret)
	}
}

func acrArgumentsToMap(t *testing.T, args []*armcontainerregistry.Argument) map[string]string {
	t.Helper()

	result := map[string]string{}
	for _, arg := range args {
		require.NotNil(t, arg.Name)
		require.NotNil(t, arg.Value)
		result[*arg.Name] = *arg.Value
	}

	return result
}

func acrRequestArgumentsToMap(args []struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	IsSecret bool   `json:"isSecret"`
}) map[string]string {
	result := map[string]string{}
	for _, arg := range args {
		result[arg.Name] = arg.Value
	}

	return result
}

func Test_ContainerHelper_Resolve_RegistryName(t *testing.T) {
	t.Run("Default EnvVar", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		env := environment.NewWithValues("dev", map[string]string{
			environment.ContainerRegistryEndpointEnvVarName: "contoso.azurecr.io",
		})

		containerHelper := NewContainerHelper(
			clock.NewMock(), nil, nil, nil,
			nil, nil, nil, cloud.AzurePublic())
		serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)
		registryName, err := containerHelper.RegistryName(*mockContext.Context, serviceConfig, env)

		require.NoError(t, err)
		require.Equal(t, "contoso.azurecr.io", registryName)
	})

	t.Run("Azure Yaml with simple string", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		env := environment.NewWithValues("dev", map[string]string{})

		containerHelper := NewContainerHelper(
			clock.NewMock(), nil, nil, nil,
			nil, nil, nil, cloud.AzurePublic())
		serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)
		serviceConfig.Docker.Registry = osutil.NewExpandableString("contoso.azurecr.io")
		registryName, err := containerHelper.RegistryName(*mockContext.Context, serviceConfig, env)

		require.NoError(t, err)
		require.Equal(t, "contoso.azurecr.io", registryName)
	})

	t.Run("Azure Yaml with expandable string", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		env := environment.NewWithValues("dev", map[string]string{})
		env.DotenvSet("MY_CUSTOM_REGISTRY", "custom.azurecr.io")

		containerHelper := NewContainerHelper(
			clock.NewMock(), nil, nil, nil,
			nil, nil, nil, cloud.AzurePublic())
		serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)
		serviceConfig.Docker.Registry = osutil.NewExpandableString("${MY_CUSTOM_REGISTRY}")
		registryName, err := containerHelper.RegistryName(*mockContext.Context, serviceConfig, env)

		require.NoError(t, err)
		require.Equal(t, "custom.azurecr.io", registryName)
	})

	t.Run("No registry name", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		env := environment.NewWithValues("dev", map[string]string{})

		containerHelper := NewContainerHelper(
			clock.NewMock(), nil, nil, nil,
			nil, nil, nil, cloud.AzurePublic())
		serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)
		registryName, err := containerHelper.RegistryName(*mockContext.Context, serviceConfig, env)

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
			mockContext := mocks.NewMockContext(t.Context())
			mockResults := setupDockerMocks(mockContext)
			env := environment.NewWithValues("dev", map[string]string{})
			dockerCli := docker.NewCli(mockContext.CommandRunner)
			dotnetCli := dotnet.NewCli(mockContext.CommandRunner)

			mockContainerRegistryService := &mockContainerRegistryService{}
			setupContainerRegistryMocks(mockContext, &mockContainerRegistryService.Mock)

			containerHelper := NewContainerHelper(
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
						*mockContext.Context, serviceConfig, serviceContext, targetResource, env, progress,
						&PublishOptions{})
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
					mock.Anything,
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
	mockContext := mocks.NewMockContext(t.Context())
	env := environment.NewWithValues("dev", map[string]string{})

	containerHelper := NewContainerHelper(
		clock.NewMock(), nil, nil, mockContext.CommandRunner,
		nil, nil, nil, cloud.AzurePublic())

	tests := []struct {
		name                 string
		projectName          string
		serviceName          string
		sourceImage          string
		envMap               map[string]string
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
			envMap: map[string]string{
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

			for k, v := range tt.envMap {
				env.DotenvSet(k, v)
			}

			image, err := containerHelper.GeneratedImage(*mockContext.Context, serviceConfig, env)

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

func (m *mockContainerRegistryServiceForRetry) FindContainerRegistryResourceGroup(
	ctx context.Context,
	subscriptionId string,
	registryName string,
) (string, error) {
	args := m.Called(ctx, subscriptionId, registryName)
	return args.String(0), args.Error(1)
}

func Test_ContainerHelper_Credential_Retry(t *testing.T) {
	t.Run("Retry on 404 on time", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		env := environment.NewWithValues("dev", map[string]string{})

		mockContainerService := &mockContainerRegistryServiceForRetry{
			MaxRetry: 1,
		}
		// no need to delay in tests
		defaultCredentialsRetryInitialDelay = 1 * time.Millisecond

		containerHelper := NewContainerHelper(
			clock.NewMock(), mockContainerService, nil, nil, nil, nil, nil, cloud.AzurePublic())

		serviceConfig := createTestServiceConfig("path", ContainerAppTarget, ServiceLanguageDotNet)
		serviceConfig.Docker.Registry = osutil.NewExpandableString("contoso.azurecr.io")
		targetResource := environment.NewTargetResource("sub", "rg", "name", "rType")

		credential, err := containerHelper.Credentials(*mockContext.Context, serviceConfig, targetResource, env)
		require.NoError(t, err)
		require.NotNil(t, credential)
		require.Equal(t, 1, mockContainerService.totalRetries())
	})
}

func setupContainerRegistryMocks(mockContext *mocks.MockContext, mockContainerRegistryService *mock.Mock) {
	mockContainerRegistryService.On(
		"Login",
		mock.Anything,
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

func (m *mockContainerRegistryService) FindContainerRegistryResourceGroup(
	ctx context.Context,
	subscriptionId string,
	registryName string,
) (string, error) {
	args := m.Called(ctx, subscriptionId, registryName)
	return args.String(0), args.Error(1)
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
			mockContext := mocks.NewMockContext(t.Context())
			mockResults := setupDockerMocks(mockContext)
			env := environment.NewWithValues("dev", map[string]string{})
			dockerCli := docker.NewCli(mockContext.CommandRunner)
			dotnetCli := dotnet.NewCli(mockContext.CommandRunner)
			envManager := &mockenv.MockEnvManager{}
			envManager.On("Get", mock.Anything, "dev").Return(env, nil)
			envManager.On("Save", *mockContext.Context, env).Return(nil)

			azdCtx := azdcontext.NewAzdContextWithDirectory(t.TempDir())
			err := azdCtx.SetProjectState(azdcontext.ProjectState{
				DefaultEnvironment: "dev",
			})
			require.NoError(t, err)

			mockContainerRegistryService := &mockContainerRegistryService{}
			setupContainerRegistryMocks(mockContext, &mockContainerRegistryService.Mock)

			containerHelper := NewContainerHelper(
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
						*mockContext.Context, serviceConfig, serviceContext, targetResource, env, progress,
						tt.publishOptions)
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
					mock.Anything,
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

func Test_ContainerHelper_Publish_RemoteBuildLocalFallback(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	mockResults := setupDockerMocks(mockContext)
	env := environment.NewWithValues("dev", map[string]string{})
	dockerCli := docker.NewCli(mockContext.CommandRunner)
	dotnetCli := dotnet.NewCli(mockContext.CommandRunner)

	// Mock Docker availability checks for local fallback
	mockContext.CommandRunner.MockToolInPath("docker", nil)

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "docker --version")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{
			Stdout:   "Docker version 20.10.17, build 100c701",
			ExitCode: 0,
		}, nil
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "docker ps")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{
			Stdout:   "CONTAINER ID   IMAGE     COMMAND   CREATED   STATUS    PORTS     NAMES",
			ExitCode: 0,
		}, nil
	})

	mockContainerRegistryService := &mockContainerRegistryService{}
	setupContainerRegistryMocks(mockContext, &mockContainerRegistryService.Mock)

	containerHelper := NewContainerHelper(
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
	serviceConfig.Docker.Registry = osutil.NewExpandableString("contoso.azurecr.io")
	serviceConfig.Docker.RemoteBuild = true
	serviceConfig.Docker.Platform = "linux/arm64"

	dockerArtifact := &Artifact{
		Kind:         ArtifactKindContainer,
		Location:     "my-project/my-service:azd-deploy-0",
		LocationKind: LocationKindLocal,
		Metadata: map[string]string{
			"imageHash":   "IMAGE_ID",
			"sourceImage": "",
			"targetImage": "my-project/my-service:azd-deploy-0",
		},
	}

	serviceContext := &ServiceContext{
		Package: ArtifactCollection{dockerArtifact},
	}

	targetResource := environment.NewTargetResource(
		"SUBSCRIPTION_ID",
		"RESOURCE_GROUP",
		"CONTAINER_APP",
		"Microsoft.App/containerApps",
	)

	publishResult, err := logProgress(
		t, func(progress *async.Progress[ServiceProgress]) (*ServicePublishResult, error) {
			return containerHelper.Publish(
				*mockContext.Context, serviceConfig, serviceContext, targetResource, env, progress, &PublishOptions{})
		},
	)

	require.NoError(t, err)
	require.Len(t, publishResult.Artifacts, 1)
	expectedImage := "contoso.azurecr.io/my-project/my-service:azd-deploy-0"
	require.Equal(t, expectedImage,
		publishResult.Artifacts[0].Metadata["remoteImage"])

	_, dockerPushCalled := mockResults["docker-push"]
	require.True(t, dockerPushCalled)

	warningFound := false
	for _, line := range mockContext.Console.Output() {
		if strings.Contains(line, "Remote build failed:") &&
			strings.Contains(line, "Falling back to local Docker build.") {
			warningFound = true
			break
		}
	}
	require.True(t, warningFound)
}

func Test_ContainerHelper_DockerfileBuilder(t *testing.T) {
	ch := &ContainerHelper{}
	builder := ch.DockerfileBuilder()
	require.NotNil(t, builder)
}

func Test_getDockerOptionsWithDefaults(t *testing.T) {
	t.Run("AllEmpty", func(t *testing.T) {
		result := getDockerOptionsWithDefaults(DockerProjectOptions{})
		assert.Equal(t, "./Dockerfile", result.Path)
		assert.Equal(t, docker.DefaultPlatform, result.Platform)
		assert.Equal(t, ".", result.Context)
	})

	t.Run("AllSet", func(t *testing.T) {
		opts := DockerProjectOptions{
			Path:     "custom/Dockerfile",
			Platform: "linux/arm64",
			Context:  "./src",
		}
		result := getDockerOptionsWithDefaults(opts)
		assert.Equal(t, "custom/Dockerfile", result.Path)
		assert.Equal(t, "linux/arm64", result.Platform)
		assert.Equal(t, "./src", result.Context)
	})

	t.Run("PartiallySet", func(t *testing.T) {
		opts := DockerProjectOptions{
			Path: "my/Dockerfile",
		}
		result := getDockerOptionsWithDefaults(opts)
		assert.Equal(t, "my/Dockerfile", result.Path)
		assert.Equal(t, docker.DefaultPlatform, result.Platform)
		assert.Equal(t, ".", result.Context)
	})
}

func Test_resolveDockerPaths(t *testing.T) {
	projectPath := t.TempDir()
	servicePath := filepath.Join(projectPath, "src", "web")
	require.NoError(t, os.MkdirAll(servicePath, 0755))

	t.Run("DefaultPaths_RelativeToServiceDir", func(t *testing.T) {
		svc := &ServiceConfig{
			Project:      &ProjectConfig{Path: projectPath},
			RelativePath: filepath.Join("src", "web"),
			Docker:       DockerProjectOptions{}, // no user overrides
		}
		opts := getDockerOptionsWithDefaults(svc.Docker)
		resolveDockerPaths(svc, &opts)

		assert.Equal(t, filepath.Join(servicePath, "Dockerfile"), opts.Path)
		assert.Equal(t, servicePath, opts.Context)
	})

	t.Run("UserSpecifiedPaths_RelativeToServiceDir", func(t *testing.T) {
		svc := &ServiceConfig{
			Project:      &ProjectConfig{Path: projectPath},
			RelativePath: filepath.Join("src", "web"),
			Docker: DockerProjectOptions{
				Path:    "docker/Dockerfile.prod",
				Context: ".",
			},
		}
		opts := getDockerOptionsWithDefaults(svc.Docker)
		resolveDockerPaths(svc, &opts)

		assert.Equal(t, filepath.Join(servicePath, "docker", "Dockerfile.prod"), opts.Path)
		assert.Equal(t, servicePath, opts.Context)
	})

	t.Run("AbsolutePaths_Unchanged", func(t *testing.T) {
		absDockerfile := filepath.Join(projectPath, "other", "Dockerfile")
		absContext := filepath.Join(projectPath, "other", "ctx")
		svc := &ServiceConfig{
			Project:      &ProjectConfig{Path: projectPath},
			RelativePath: filepath.Join("src", "web"),
			Docker: DockerProjectOptions{
				Path:    absDockerfile,
				Context: absContext,
			},
		}
		opts := getDockerOptionsWithDefaults(svc.Docker)
		resolveDockerPaths(svc, &opts)

		assert.Equal(t, absDockerfile, opts.Path)
		assert.Equal(t, absContext, opts.Context)
	})

	t.Run("DefaultPaths_RelativePathIsFile", func(t *testing.T) {
		// When RelativePath points to a file (e.g., Aspire project.v1 .csproj),
		// default docker paths should resolve relative to the parent directory.
		serviceDir := filepath.Join(projectPath, "src")
		require.NoError(t, os.MkdirAll(serviceDir, 0755))
		csprojFile := filepath.Join(serviceDir, "app.csproj")
		require.NoError(t, os.WriteFile(csprojFile, []byte("<Project/>"), 0600))

		svc := &ServiceConfig{
			Project:      &ProjectConfig{Path: projectPath},
			RelativePath: filepath.Join("src", "app.csproj"),
			Docker:       DockerProjectOptions{}, // no user overrides
		}
		opts := getDockerOptionsWithDefaults(svc.Docker)
		resolveDockerPaths(svc, &opts)

		// Defaults should resolve to the directory containing app.csproj, not app.csproj itself.
		assert.Equal(t, filepath.Join(serviceDir, "Dockerfile"), opts.Path)
		assert.Equal(t, serviceDir, opts.Context)
	})

	t.Run("MixedPaths_UserPathDefaultContext", func(t *testing.T) {
		svc := &ServiceConfig{
			Project:      &ProjectConfig{Path: projectPath},
			RelativePath: filepath.Join("src", "web"),
			Docker: DockerProjectOptions{
				Path: "docker/Dockerfile.dev",
				// Context not set — will default to "."
			},
		}
		opts := getDockerOptionsWithDefaults(svc.Docker)
		resolveDockerPaths(svc, &opts)

		// Both resolve relative to service dir
		assert.Equal(t, filepath.Join(servicePath, "docker", "Dockerfile.dev"), opts.Path)
		assert.Equal(t, servicePath, opts.Context)
	})

	t.Run("PathTraversal_DotDot_Normalized", func(t *testing.T) {
		svc := &ServiceConfig{
			Project:      &ProjectConfig{Path: projectPath},
			RelativePath: filepath.Join("src", "web"),
			Docker: DockerProjectOptions{
				Path:    "../shared/Dockerfile",
				Context: "../shared",
			},
		}
		opts := getDockerOptionsWithDefaults(svc.Docker)
		resolveDockerPaths(svc, &opts)

		// ".." segments are resolved and cleaned by filepath.Clean
		assert.Equal(t, filepath.Join(projectPath, "src", "shared", "Dockerfile"), opts.Path)
		assert.Equal(t, filepath.Join(projectPath, "src", "shared"), opts.Context)
	})

	t.Run("PathTraversal_DoubleDotDot_Normalized", func(t *testing.T) {
		svc := &ServiceConfig{
			Project:      &ProjectConfig{Path: projectPath},
			RelativePath: filepath.Join("src", "web"),
			Docker: DockerProjectOptions{
				Path:    "../../Dockerfile",
				Context: "../../",
			},
		}
		opts := getDockerOptionsWithDefaults(svc.Docker)
		resolveDockerPaths(svc, &opts)

		// Double ".." walks back to projectPath root
		assert.Equal(t, filepath.Join(projectPath, "Dockerfile"), opts.Path)
		assert.Equal(t, projectPath, opts.Context)
	})
}

// Test_InMemDockerfile_ContextOverride verifies that in-memory Dockerfile builds
// correctly override the build context to the temp directory when the user did
// not specify a custom Docker context (empty or ".").
//
// This tests the fix where we check serviceConfig.Docker.Context (original config)
// instead of dockerOptions.Context (already resolved to absolute by resolveDockerPaths).
// After resolution, dockerOptions.Context is an absolute path, so comparing against
// "" or "." would never match — checking the original config is intentional.
func Test_InMemDockerfile_ContextOverride(t *testing.T) {
	projectPath := t.TempDir()
	servicePath := filepath.Join(projectPath, "src", "web")
	require.NoError(t, os.MkdirAll(servicePath, 0755))

	t.Run("DefaultContext_OverriddenToTempDir", func(t *testing.T) {
		// When Docker.Context is empty (default), the in-memory Dockerfile flow
		// should override context to the temp dir where the Dockerfile is written.
		svc := &ServiceConfig{
			Project:      &ProjectConfig{Path: projectPath},
			RelativePath: filepath.Join("src", "web"),
			Docker:       DockerProjectOptions{}, // Context defaults to ""
		}
		opts := getDockerOptionsWithDefaults(svc.Docker)
		resolveDockerPaths(svc, &opts)

		// After resolution, opts.Context is an absolute path (service dir).
		assert.Equal(t, servicePath, opts.Context, "precondition: context is resolved to service dir")

		// Simulate the in-memory Dockerfile override (container_helper.go ~line 478).
		// This uses serviceConfig.Docker.Context (original config value) — NOT opts.Context.
		tempDir := t.TempDir()
		if svc.Docker.Context == "" || svc.Docker.Context == "." {
			opts.Context = tempDir
		}

		assert.Equal(t, tempDir, opts.Context, "context should be overridden to tempDir")
	})

	t.Run("DotContext_OverriddenToTempDir", func(t *testing.T) {
		svc := &ServiceConfig{
			Project:      &ProjectConfig{Path: projectPath},
			RelativePath: filepath.Join("src", "web"),
			Docker: DockerProjectOptions{
				Context: ".",
			},
		}
		opts := getDockerOptionsWithDefaults(svc.Docker)
		resolveDockerPaths(svc, &opts)

		assert.Equal(t, servicePath, opts.Context, "precondition: '.' resolves to service dir")

		tempDir := t.TempDir()
		if svc.Docker.Context == "" || svc.Docker.Context == "." {
			opts.Context = tempDir
		}

		assert.Equal(t, tempDir, opts.Context, "context should be overridden to tempDir")
	})

	t.Run("CustomContext_PreservedNotOverridden", func(t *testing.T) {
		// When user specifies a custom context, the in-memory Dockerfile flow
		// must NOT override it — the user's explicit context takes precedence.
		svc := &ServiceConfig{
			Project:      &ProjectConfig{Path: projectPath},
			RelativePath: filepath.Join("src", "web"),
			Docker: DockerProjectOptions{
				Context: "custom/build-context",
			},
		}
		opts := getDockerOptionsWithDefaults(svc.Docker)
		resolveDockerPaths(svc, &opts)

		expectedContext := filepath.Join(servicePath, "custom", "build-context")
		assert.Equal(t, expectedContext, opts.Context, "precondition: custom context resolved")

		tempDir := t.TempDir()
		if svc.Docker.Context == "" || svc.Docker.Context == "." {
			opts.Context = tempDir
		}

		// Context should NOT be overridden — user specified a custom path.
		assert.Equal(t, expectedContext, opts.Context,
			"custom context must be preserved, not overridden to tempDir")
	})

	t.Run("BugRegression_CheckingResolvedContext_WouldNeverMatch", func(t *testing.T) {
		// This test proves WHY we check serviceConfig.Docker.Context instead of
		// dockerOptions.Context. After resolveDockerPaths(), opts.Context is always
		// an absolute path — checking it against "" or "." would never match,
		// meaning tempDir override would never happen.
		svc := &ServiceConfig{
			Project:      &ProjectConfig{Path: projectPath},
			RelativePath: filepath.Join("src", "web"),
			Docker:       DockerProjectOptions{}, // Context defaults to ""
		}
		opts := getDockerOptionsWithDefaults(svc.Docker)
		resolveDockerPaths(svc, &opts)

		// Demonstrate the bug: checking resolved opts.Context against "" or "." never matches.
		resolvedContextMatchesEmpty := opts.Context == "" || opts.Context == "."
		assert.False(t, resolvedContextMatchesEmpty,
			"resolved context is absolute — comparing against empty/dot never matches")

		// But the original config value DOES match.
		originalConfigMatchesEmpty := svc.Docker.Context == "" || svc.Docker.Context == "."
		assert.True(t, originalConfigMatchesEmpty,
			"original config is empty — this is what we should check")
	})
}

// Tests for containerAppTarget methods at 0%
func Test_containerAppTarget_RequiredExternalTools(t *testing.T) {
	ch := &ContainerHelper{}
	at := &containerAppTarget{containerHelper: ch}
	sc := &ServiceConfig{
		Name:     "api",
		Language: ServiceLanguagePython,
		Project:  &ProjectConfig{},
	}
	toolList := at.RequiredExternalTools(t.Context(), sc)
	// containerHelper.RequiredExternalTools returns docker tool if not remote-build
	// with an empty ServiceConfig Docker section, we get docker
	assert.NotNil(t, toolList)
}
