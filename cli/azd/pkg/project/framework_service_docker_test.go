// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/npm"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockarmresources"
	"github.com/benbjohnson/clock"
	"github.com/stretchr/testify/require"
)

func saveMessages(msg *[]string) func(s string) {
	return func(s string) {
		*msg = append(*msg, s)
	}
}

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

	env := environment.EphemeralWithValues("test-env", nil)
	env.SetSubscriptionId("sub")

	mockContext := mocks.NewMockContext(context.Background())

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

		require.Equal(t, []string{
			"build", "-q",
			"-f", "./Dockerfile",
			"--platform", docker.DefaultPlatform,
			"-t", "test-proj-web",
			".",
		}, args.Args)

		return exec.RunResult{
			Stdout:   "imageId",
			Stderr:   "",
			ExitCode: 0,
		}, nil
	})

	projectConfig, err := Parse(*mockContext.Context, testProj)
	require.NoError(t, err)
	service := projectConfig.Services["web"]

	npmCli := npm.NewNpmCli(mockContext.CommandRunner)
	docker := docker.NewDocker(mockContext.CommandRunner)

	internalFramework := NewNpmProject(npmCli, env)

	framework := NewDockerProject(env, docker, NewContainerHelper(env, clock.NewMock(), nil, docker))
	framework.SetSource(internalFramework)

	progressMessages := []string{}
	showProgress := saveMessages(&progressMessages)
	res, err := framework.Build(*mockContext.Context, service, nil, showProgress)

	require.Equal(t, "imageId", res.BuildOutputPath)
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

	env := environment.EphemeralWithValues("test-env", nil)
	env.SetSubscriptionId("sub")
	mockContext := mocks.NewMockContext(context.Background())

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

		require.Equal(t, []string{
			"build", "-q",
			"-f", "./Dockerfile.dev",
			"--platform", docker.DefaultPlatform,
			"-t", "test-proj-web",
			"../",
		}, args.Args)

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
	internalFramework := NewNpmProject(npmCli, env)
	framework := NewDockerProject(env, docker, NewContainerHelper(env, clock.NewMock(), nil, docker))
	framework.SetSource(internalFramework)

	messages := []string{}
	showProgress := saveMessages(&messages)
	res, err := framework.Build(*mockContext.Context, service, nil, showProgress)

	require.Equal(t, "imageId", res)
	require.Nil(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, "Building Docker image", messages[0])
	require.Equal(t, true, ran)
}

func Test_DockerProject_Build(t *testing.T) {
	var runArgs exec.RunArgs

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.
		When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "docker build")
		}).
		RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			runArgs = args
			return exec.NewRunResult(0, "IMAGE_ID", ""), nil
		})

	env := environment.Ephemeral()
	dockerCli := docker.NewDocker(mockContext.CommandRunner)
	serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)

	dockerProject := NewDockerProject(env, dockerCli, NewContainerHelper(env, clock.NewMock(), nil, dockerCli))

	messages := []string{}
	showProgress := saveMessages(&messages)
	res, err := dockerProject.Build(*mockContext.Context, serviceConfig, nil, showProgress)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, "IMAGE_ID", res.BuildOutputPath)
	require.Equal(t, "docker", runArgs.Cmd)
	require.Equal(t, serviceConfig.RelativePath, runArgs.Cwd)
	require.Equal(t,
		[]string{
			"build", "-q",
			"-f", "./Dockerfile",
			"--platform", docker.DefaultPlatform,
			"-t", "test-app-api",
			".",
		},
		runArgs.Args,
	)

	dockerBuildResult, ok := res.Details.(*dockerBuildResult)
	require.True(t, ok)
	require.NotNil(t, dockerBuildResult)
	require.Equal(t, "test-app-api", dockerBuildResult.ImageName)
	require.NotEmpty(t, dockerBuildResult.ImageId)
}

func Test_DockerProject_Package(t *testing.T) {
	var runArgs exec.RunArgs

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.
		When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "docker tag")
		}).
		RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			runArgs = args
			return exec.NewRunResult(0, "IMAGE_ID", ""), nil
		})

	env := environment.EphemeralWithValues("test", map[string]string{})
	dockerCli := docker.NewDocker(mockContext.CommandRunner)
	serviceConfig := createTestServiceConfig("./src/api", ContainerAppTarget, ServiceLanguageTypeScript)

	dockerProject := NewDockerProject(env, dockerCli, NewContainerHelper(env, clock.NewMock(), nil, dockerCli))
	messages := []string{}
	showProgress := saveMessages(&messages)
	res, err := dockerProject.Package(
		*mockContext.Context,
		serviceConfig,
		&ServiceBuildResult{
			BuildOutputPath: "IMAGE_ID",
		},
		showProgress,
	)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.IsType(t, new(dockerPackageResult), res.Details)

	packageResult, ok := res.Details.(*dockerPackageResult)
	require.Equal(t, "test-app/api-test:azd-deploy-0", res.PackagePath)

	require.True(t, ok)
	require.Equal(t, "test-app/api-test:azd-deploy-0", packageResult.ImageTag)

	require.Equal(t, "docker", runArgs.Cmd)
	require.Equal(t, serviceConfig.RelativePath, runArgs.Cwd)
	require.Equal(t,
		[]string{"tag", "IMAGE_ID", "test-app/api-test:azd-deploy-0"},
		runArgs.Args,
	)
}
