package project

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/executil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/azure/azure-dev/cli/azd/test/helpers"
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
`

	ctx := helpers.CreateTestContext(context.Background(), gblCmdOptions, azCli, mockHttpClient)
	env := environment.Environment{Values: make(map[string]string)}
	env.SetEnvName("test-env")

	projectConfig, _ := ParseProjectConfig(testProj, &env)
	prj, _ := projectConfig.GetProject(ctx, &env)
	service := prj.Services[0]
	ran := false

	dockerArgs := docker.DockerArgs{
		RunWithResultFn: func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
			ran = true

			require.Equal(t, []string{
				"build", "-q",
				"-f", "./Dockerfile",
				"--platform", "amd64",
				".",
			}, args.Args)

			return executil.RunResult{
				Stdout:   "imageId",
				Stderr:   "",
				ExitCode: 0,
			}, nil
		},
	}

	docker := docker.NewDocker(dockerArgs)

	progress := make(chan string)
	done := make(chan bool)

	internalFramework := NewNpmProject(service.Config, &env)
	progressMessages := []string{}

	go func() {
		for value := range progress {
			progressMessages = append(progressMessages, value)
		}
		done <- true
	}()

	framework := NewDockerProject(service.Config, &env, docker, internalFramework)
	res, err := framework.Package(ctx, progress)
	close(progress)
	<-done

	require.Equal(t, "imageId", res)
	require.Nil(t, err)
	require.Len(t, progressMessages, 1)
	require.Equal(t, "Building docker image", progressMessages[0])
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
    docker:
      path: ./Dockerfile.dev
      context: ../
`

	ctx := helpers.CreateTestContext(context.Background(), gblCmdOptions, azCli, mockHttpClient)
	env := environment.Environment{Values: make(map[string]string)}
	env.SetEnvName("test-env")

	projectConfig, _ := ParseProjectConfig(testProj, &env)
	prj, _ := projectConfig.GetProject(ctx, &env)
	service := prj.Services[0]
	ran := false

	dockerArgs := docker.DockerArgs{
		RunWithResultFn: func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
			ran = true

			require.Equal(t, []string{
				"build", "-q",
				"-f", "./Dockerfile.dev",
				"--platform", "amd64",
				"../",
			}, args.Args)

			return executil.RunResult{
				Stdout:   "imageId",
				Stderr:   "",
				ExitCode: 0,
			}, nil
		},
	}

	docker := docker.NewDocker(dockerArgs)

	progress := make(chan string)
	done := make(chan bool)

	internalFramework := NewNpmProject(service.Config, &env)
	status := ""

	go func() {
		for value := range progress {
			status = value
		}
		done <- true
	}()

	framework := NewDockerProject(service.Config, &env, docker, internalFramework)
	res, err := framework.Package(ctx, progress)
	close(progress)
	<-done

	require.Equal(t, "imageId", res)
	require.Nil(t, err)
	require.Equal(t, "Building docker image", status)
	require.Equal(t, true, ran)
}
