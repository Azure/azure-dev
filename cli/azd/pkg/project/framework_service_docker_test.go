// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/npm"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockarmresources"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/benbjohnson/clock"
	"github.com/stretchr/testify/require"
)

func TestDefaultDockerOptions(t *testing.T) {
	const testProj = `
name: test-proj
metadata:
  template: test-proj-template
resourceGroup: rg-test
services:
  web:
    project: src/web
    language: js
    host: containerapp
    resourceName: test-containerapp-web
`
	ran := false

	env := environment.NewWithValues("test-env", nil)
	env.SetSubscriptionId("sub")

	mockContext := mocks.NewMockContext(context.Background())
	envManager := &mockenv.MockEnvManager{}

	mockarmresources.AddAzResourceListMock(
		mockContext.HttpClient,
		convert.RefOf("rg-test"),
		[]*armresources.GenericResourceExpanded{
			{
				ID:       convert.RefOf("app-api-abc123"),
				Name:     convert.RefOf("test-containerapp-web"),
				Type:     convert.RefOf(string(infra.AzureResourceTypeContainerApp)),
				Location: convert.RefOf("eastus2"),
			},
		})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "docker build")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		ran = true

		// extract img id file arg. "--iidfile" and path args are expected always at the end
		argsNoFile, value := args.Args[:len(args.Args)-2], args.Args[len(args.Args)-1]

		require.Equal(t, []string{
			"build",
			"-f", "./Dockerfile",
			"--platform", docker.DefaultPlatform,
			"-t", "test-proj-web",
			".",
		}, argsNoFile)

		// create the file as expected
		err := os.WriteFile(value, []byte("imageId"), 0600)
		require.NoError(t, err)

		return exec.RunResult{
			Stdout:   "imageId",
			Stderr:   "",
			ExitCode: 0,
		}, nil
	})

	projectConfig, err := Parse(*mockContext.Context, testProj)
	require.NoError(t, err)
	service := projectConfig.Services["web"]

	temp := t.TempDir()
	service.Project.Path = temp
	service.RelativePath = ""
	err = os.WriteFile(filepath.Join(temp, "Dockerfile"), []byte("FROM node:14"), 0600)
	require.NoError(t, err)

	npmCli := npm.NewNpmCli(mockContext.CommandRunner)
	docker := docker.NewDocker(mockContext.CommandRunner)

	done := make(chan bool)

	internalFramework := NewNpmProject(npmCli, env)
	progressMessages := []string{}

	framework := NewDockerProject(
		env,
		docker,
		NewContainerHelper(env, envManager, clock.NewMock(), nil, docker, cloud.AzurePublic()),
		mockinput.NewMockConsole(),
		mockContext.AlphaFeaturesManager,
		mockContext.CommandRunner)
	framework.SetSource(internalFramework)

	buildTask := framework.Build(*mockContext.Context, service, nil)
	go func() {
		for value := range buildTask.Progress() {
			progressMessages = append(progressMessages, value.Message)
		}
		done <- true
	}()

	buildResult, err := buildTask.Await()
	<-done

	require.Equal(t, "imageId", buildResult.BuildOutputPath)
	require.Nil(t, err)
	require.Len(t, progressMessages, 1)
	require.Equal(t, "Building Docker image", progressMessages[0])
	require.Equal(t, true, ran)
}

func TestCustomDockerOptions(t *testing.T) {
	const testProj = `
name: test-proj
metadata:
  template: test-proj-template
resourceGroup: rg-test
services:
  web:
    project: src/web
    language: js
    host: containerapp
    resourceName: test-containerapp-web
    docker:
      path: ./Dockerfile.dev
      context: ../
`

	env := environment.NewWithValues("test-env", nil)
	env.SetSubscriptionId("sub")
	mockContext := mocks.NewMockContext(context.Background())
	envManager := &mockenv.MockEnvManager{}

	mockarmresources.AddAzResourceListMock(
		mockContext.HttpClient,
		convert.RefOf("rg-test"),
		[]*armresources.GenericResourceExpanded{
			{
				ID:       convert.RefOf("app-api-abc123"),
				Name:     convert.RefOf("test-containerapp-web"),
				Type:     convert.RefOf(string(infra.AzureResourceTypeContainerApp)),
				Location: convert.RefOf("eastus2"),
			},
		})

	ran := false

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "docker build")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		ran = true

		// extract img id file arg. "--iidfile" and path args are expected always at the end
		argsNoFile, value := args.Args[:len(args.Args)-2], args.Args[len(args.Args)-1]

		require.Equal(t, []string{
			"build",
			"-f", "./Dockerfile.dev",
			"--platform", docker.DefaultPlatform,
			"-t", "test-proj-web",
			"../",
		}, argsNoFile)

		// create the file as expected
		err := os.WriteFile(value, []byte("imageId"), 0600)
		require.NoError(t, err)

		return exec.RunResult{
			Stdout:   "imageId",
			Stderr:   "",
			ExitCode: 0,
		}, nil
	})

	npmCli := npm.NewNpmCli(mockContext.CommandRunner)
	docker := docker.NewDocker(mockContext.CommandRunner)

	projectConfig, err := Parse(*mockContext.Context, testProj)
	require.NoError(t, err)

	service := projectConfig.Services["web"]
	temp := t.TempDir()
	service.Project.Path = temp
	service.RelativePath = ""
	err = os.WriteFile(filepath.Join(temp, "Dockerfile.dev"), []byte("FROM node:14"), 0600)
	require.NoError(t, err)

	done := make(chan bool)

	internalFramework := NewNpmProject(npmCli, env)
	status := ""

	framework := NewDockerProject(
		env,
		docker,
		NewContainerHelper(env, envManager, clock.NewMock(), nil, docker, cloud.AzurePublic()),
		mockinput.NewMockConsole(),
		mockContext.AlphaFeaturesManager,
		mockContext.CommandRunner)
	framework.SetSource(internalFramework)

	buildTask := framework.Build(*mockContext.Context, service, nil)
	go func() {
		for value := range buildTask.Progress() {
			status = value.Message
		}
		done <- true
	}()

	buildResult, err := buildTask.Await()
	<-done

	require.Equal(t, "imageId", buildResult.BuildOutputPath)
	require.Nil(t, err)
	require.Equal(t, "Building Docker image", status)
	require.Equal(t, true, ran)
}

func Test_DockerProject_Build(t *testing.T) {
	tests := []struct {
		name                    string
		project                 string
		language                ServiceLanguageKind
		dockerOptions           DockerProjectOptions
		hasDockerFile           bool
		image                   string
		expectedBuildResult     *ServiceBuildResult
		expectedDockerBuildArgs []string
	}{
		{
			name:          "With language TS and docker defaults (standard project)",
			project:       "./src/api",
			language:      ServiceLanguageJavaScript,
			hasDockerFile: true,
			expectedBuildResult: &ServiceBuildResult{
				BuildOutputPath: "IMAGE_ID",
				Details: &dockerBuildResult{
					ImageName: "test-app-api",
					ImageId:   "IMAGE_ID",
				},
			},
			expectedDockerBuildArgs: []string{
				"build",
				"-f",
				"./Dockerfile",
				"--platform",
				"linux/amd64",
				"-t",
				"test-app-api",
				".",
			},
		},
		{
			name:          "With language TS and custom docker options (standard project)",
			project:       "./src/api",
			language:      ServiceLanguageJavaScript,
			hasDockerFile: true,
			dockerOptions: DockerProjectOptions{
				Path:     "./Dockerfile.dev",
				Context:  "../",
				Platform: "custom/platform",
				Target:   "custom-target",
			},
			expectedBuildResult: &ServiceBuildResult{
				BuildOutputPath: "IMAGE_ID",
				Details: &dockerBuildResult{
					ImageName: "test-app-api",
					ImageId:   "IMAGE_ID",
				},
			},
			expectedDockerBuildArgs: []string{
				"build",
				"-f",
				"./Dockerfile.dev",
				"--platform",
				"custom/platform",
				"--target",
				"custom-target",
				"-t",
				"test-app-api",
				"../",
			},
		},
		{
			name:          "With language Docker and docker defaults (aspire project)",
			project:       "./src/api",
			language:      ServiceLanguageDocker,
			hasDockerFile: true,
			expectedBuildResult: &ServiceBuildResult{
				BuildOutputPath: "IMAGE_ID",
				Details: &dockerBuildResult{
					ImageName: "test-app-api",
					ImageId:   "IMAGE_ID",
				},
			},
			expectedDockerBuildArgs: []string{
				"build",
				"-f",
				"./Dockerfile",
				"--platform",
				"linux/amd64",
				"-t",
				"test-app-api",
				".",
			},
		},
		{
			name:                    "With no language and docker defaults (external image)",
			project:                 "",
			language:                ServiceLanguageNone,
			hasDockerFile:           false,
			image:                   "nginx",
			expectedBuildResult:     &ServiceBuildResult{},
			expectedDockerBuildArgs: nil,
		},
		{
			name:          "With language and no docker file (pack)",
			project:       "./src/api",
			language:      ServiceLanguageJavaScript,
			hasDockerFile: false,
			expectedBuildResult: &ServiceBuildResult{
				BuildOutputPath: "IMAGE_ID",
				Details: &dockerBuildResult{
					ImageName: "test-app-api",
					ImageId:   "IMAGE_ID",
				},
			},
			expectedDockerBuildArgs: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var dockerBuildArgs exec.RunArgs
			mockContext := mocks.NewMockContext(context.Background())
			envManager := &mockenv.MockEnvManager{}

			mockContext.CommandRunner.
				When(func(args exec.RunArgs, command string) bool {
					return strings.Contains(command, "docker build")
				}).
				RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
					// extract img id file arg. "--iidfile" and path args are expected always at the end
					argsNoFile, value := args.Args[:len(args.Args)-2], args.Args[len(args.Args)-1]
					dockerBuildArgs = args
					dockerBuildArgs.Args = argsNoFile
					// create the file as expected
					err := os.WriteFile(value, []byte("IMAGE_ID"), 0600)
					require.NoError(t, err)
					return exec.NewRunResult(0, "IMAGE_ID", ""), nil
				})

			mockContext.CommandRunner.
				When(func(args exec.RunArgs, command string) bool {
					return strings.Contains(command, "docker image inspect")
				}).
				RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
					return exec.NewRunResult(0, "IMAGE_ID", ""), nil
				})

			mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "pack")
			}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				return exec.NewRunResult(0, "", ""), nil
			})

			mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "pack") && len(args.Args) == 1 && args.Args[0] == "--version"
			}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				return exec.NewRunResult(0, "3.0.0", ""), nil
			})

			temp := t.TempDir()

			env := environment.New("test")
			dockerCli := docker.NewDocker(mockContext.CommandRunner)
			serviceConfig := createTestServiceConfig(tt.project, ContainerAppTarget, tt.language)
			serviceConfig.Project.Path = temp
			serviceConfig.Docker = tt.dockerOptions
			serviceConfig.Image = tt.image

			if tt.hasDockerFile {
				err := os.MkdirAll(serviceConfig.Path(), osutil.PermissionDirectory)
				require.NoError(t, err)

				dockerFilePath := "Dockerfile"
				if serviceConfig.Docker.Path != "" {
					dockerFilePath = serviceConfig.Docker.Path
				}

				err = os.WriteFile(filepath.Join(serviceConfig.Path(), dockerFilePath), []byte("FROM node:14"), 0600)
				require.NoError(t, err)
			}

			dockerProject := NewDockerProject(
				env,
				dockerCli,
				NewContainerHelper(env, envManager, clock.NewMock(), nil, dockerCli, cloud.AzurePublic()),
				mockinput.NewMockConsole(),
				mockContext.AlphaFeaturesManager,
				mockContext.CommandRunner)

			if tt.language == ServiceLanguageTypeScript || tt.language == ServiceLanguageJavaScript {
				npmProject := NewNpmProject(npm.NewNpmCli(mockContext.CommandRunner), env)
				dockerProject.SetSource(npmProject)
			}

			buildTask := dockerProject.Build(*mockContext.Context, serviceConfig, nil)
			logProgress(buildTask)
			result, err := buildTask.Await()

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, tt.expectedBuildResult, result)
			require.Equal(t, tt.expectedDockerBuildArgs, dockerBuildArgs.Args)
		})
	}
}

func Test_DockerProject_Package(t *testing.T) {
	tests := []struct {
		name                   string
		image                  string
		project                string
		docker                 DockerProjectOptions
		expectedPackageResult  dockerPackageResult
		expectDockerPullCalled bool
		expectDockerTagCalled  bool
	}{
		{
			name:    "source with defaults",
			project: "./src/api",
			expectedPackageResult: dockerPackageResult{
				ImageHash:   "IMAGE_ID",
				SourceImage: "",
				TargetImage: "test-app/api-test:azd-deploy-0",
			},
			expectDockerPullCalled: false,
			expectDockerTagCalled:  true,
		},
		{
			name:    "source with custom docker options",
			project: "./src/api",
			docker: DockerProjectOptions{
				Image: osutil.NewExpandableString("foo/bar"),
				Tag:   osutil.NewExpandableString("latest"),
			},
			expectedPackageResult: dockerPackageResult{
				ImageHash:   "IMAGE_ID",
				SourceImage: "",
				TargetImage: "foo/bar:latest",
			},
			expectDockerPullCalled: false,
			expectDockerTagCalled:  true,
		},
		{
			name:  "image with defaults",
			image: "nginx:latest",
			expectedPackageResult: dockerPackageResult{
				ImageHash:   "",
				SourceImage: "nginx:latest",
				TargetImage: "test-app/api-test:azd-deploy-0",
			},
			expectDockerPullCalled: true,
			expectDockerTagCalled:  true,
		},
		{
			name:  "image with custom docker options",
			image: "nginx:latest",
			docker: DockerProjectOptions{
				Image: osutil.NewExpandableString("foo/bar"),
				Tag:   osutil.NewExpandableString("latest"),
			},
			expectedPackageResult: dockerPackageResult{
				ImageHash:   "",
				SourceImage: "nginx:latest",
				TargetImage: "foo/bar:latest",
			},
			expectDockerPullCalled: true,
			expectDockerTagCalled:  true,
		},
		{
			name:  "fully qualified image with custom docker options",
			image: "docker.io/repository/image:latest",
			docker: DockerProjectOptions{
				Image: osutil.NewExpandableString("myapp-service"),
				Tag:   osutil.NewExpandableString("latest"),
			},
			expectedPackageResult: dockerPackageResult{
				ImageHash:   "",
				SourceImage: "docker.io/repository/image:latest",
				TargetImage: "myapp-service:latest",
			},
			expectDockerPullCalled: true,
			expectDockerTagCalled:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(context.Background())
			mockResults := setupDockerMocks(mockContext)
			envManager := &mockenv.MockEnvManager{}

			env := environment.NewWithValues("test", map[string]string{})
			dockerCli := docker.NewDocker(mockContext.CommandRunner)
			serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)

			dockerProject := NewDockerProject(
				env,
				dockerCli,
				NewContainerHelper(env, envManager, clock.NewMock(), nil, dockerCli, cloud.AzurePublic()),
				mockinput.NewMockConsole(),
				mockContext.AlphaFeaturesManager,
				mockContext.CommandRunner)

			// Set the custom test options
			serviceConfig.Docker = tt.docker
			serviceConfig.RelativePath = tt.project
			serviceConfig.Image = tt.image

			if serviceConfig.RelativePath != "" {
				npmProject := NewNpmProject(npm.NewNpmCli(mockContext.CommandRunner), env)
				dockerProject.SetSource(npmProject)
			}

			buildOutputPath := ""
			if serviceConfig.Image == "" && serviceConfig.RelativePath != "" {
				buildOutputPath = "IMAGE_ID"
			}

			packageTask := dockerProject.Package(
				*mockContext.Context,
				serviceConfig,
				&ServiceBuildResult{
					BuildOutputPath: buildOutputPath,
				},
			)
			logProgress(packageTask)

			result, err := packageTask.Await()
			require.NoError(t, err)
			dockerDetails, ok := result.Details.(*dockerPackageResult)
			require.True(t, ok)
			require.Equal(t, tt.expectedPackageResult, *dockerDetails)

			_, dockerPullCalled := mockResults["docker-pull"]
			_, dockerTagCalled := mockResults["docker-tag"]

			require.Equal(t, tt.expectDockerPullCalled, dockerPullCalled)
			require.Equal(t, tt.expectDockerTagCalled, dockerTagCalled)
		})
	}
}
