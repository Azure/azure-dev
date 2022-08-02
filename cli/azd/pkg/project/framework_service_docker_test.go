package project

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/executil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
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

	dockerArgs := tools.DockerArgs{
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

	docker := tools.NewDocker(dockerArgs)

	progress := make(chan string)
	defer close(progress)

	internalFramework := NewNpmProject(service.Config, &env)
	status := ""

	go func() {
		for value := range progress {
			status = value
		}
	}()

	framework := NewDockerProject(service.Config, &env, docker, internalFramework)
	res, err := framework.Package(ctx, progress)

	require.Equal(t, "imageId", res)
	require.Nil(t, err)
	require.Equal(t, "Building docker image", status)
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

	dockerArgs := tools.DockerArgs{
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

	docker := tools.NewDocker(dockerArgs)

	progress := make(chan string)
	defer close(progress)

	internalFramework := NewNpmProject(service.Config, &env)
	status := ""

	go func() {
		for value := range progress {
			status = value
		}
	}()

	framework := NewDockerProject(service.Config, &env, docker, internalFramework)
	res, err := framework.Package(ctx, progress)

	require.Equal(t, "imageId", res)
	require.Nil(t, err)
	require.Equal(t, "Building docker image", status)
	require.Equal(t, true, ran)
}
