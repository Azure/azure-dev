// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestBicepOutputsWithDoubleUnderscoresAreConverted(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	keys := []string{}

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "dotnet user-secrets set")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		t.Logf("dotnet user-secrets set was called with: %+v", args)
		keys = append(keys, args.Args[2])
		return exec.NewRunResult(0, "", ""), nil
	})

	serviceConfig := &ServiceConfig{
		Project: &ProjectConfig{
			Path: "/sample/path/for/test",
		},
		RelativePath: "",
	}

	dotNetCli := dotnet.NewDotNetCli(mockContext.CommandRunner)
	dp := NewDotNetProject(dotNetCli, environment.Ephemeral()).(*dotnetProject)

	err := dp.setUserSecretsFromOutputs(*mockContext.Context, serviceConfig, ServiceLifecycleEventArgs{
		Args: map[string]any{
			"bicepOutput": map[string]provisioning.OutputParameter{
				"EXAMPLE_OUTPUT":          {Type: "string", Value: "foo"},
				"EXAMPLE__NESTED__OUTPUT": {Type: "string", Value: "bar"},
			},
		},
	})

	require.NoError(t, err)
	require.Len(t, keys, 2)

	sort.Strings(keys)
	require.Equal(t, "EXAMPLE:NESTED:OUTPUT", keys[0])
	require.Equal(t, "EXAMPLE_OUTPUT", keys[1])
}

func Test_DotNetProject_Restore(t *testing.T) {
	var runArgs exec.RunArgs

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.
		When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "dotnet restore")
		}).
		RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			runArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

	env := environment.Ephemeral()
	dotNetCli := dotnet.NewDotNetCli(mockContext.CommandRunner)
	serviceConfig := createTestServiceConfig("./src/api", AppServiceTarget, ServiceLanguageCsharp)

	dotnetProject := NewDotNetProject(dotNetCli, env)
	restoreTask := dotnetProject.Restore(*mockContext.Context, serviceConfig)
	logProgress(restoreTask)

	result, err := restoreTask.Await()
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "dotnet", runArgs.Cmd)
	require.Equal(t,
		[]string{"restore", serviceConfig.RelativePath},
		runArgs.Args,
	)
}

func Test_DotNetProject_Build(t *testing.T) {
	var runArgs exec.RunArgs

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.
		When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "dotnet build")
		}).
		RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			runArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

	env := environment.Ephemeral()
	dotNetCli := dotnet.NewDotNetCli(mockContext.CommandRunner)
	serviceConfig := createTestServiceConfig("./src/api", AppServiceTarget, ServiceLanguageCsharp)

	dotnetProject := NewDotNetProject(dotNetCli, env)
	buildTask := dotnetProject.Build(*mockContext.Context, serviceConfig, nil)
	logProgress(buildTask)

	result, err := buildTask.Await()
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "dotnet", runArgs.Cmd)
	require.Equal(t,
		[]string{"build", serviceConfig.RelativePath, "-c", "Release"},
		runArgs.Args,
	)
}

func Test_DotNetProject_Package(t *testing.T) {
	var runArgs exec.RunArgs

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.
		When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "dotnet publish")
		}).
		RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			runArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

	env := environment.Ephemeral()
	dotNetCli := dotnet.NewDotNetCli(mockContext.CommandRunner)
	serviceConfig := createTestServiceConfig("./src/api", AppServiceTarget, ServiceLanguageCsharp)

	dotnetProject := NewDotNetProject(dotNetCli, env)
	packageTask := dotnetProject.Package(
		*mockContext.Context,
		serviceConfig,
		&ServiceBuildResult{
			BuildOutputPath: serviceConfig.Path(),
		},
	)
	logProgress(packageTask)

	result, err := packageTask.Await()
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.PackagePath)
	require.Equal(t, "dotnet", runArgs.Cmd)
	require.Equal(t,
		[]string{"publish", serviceConfig.RelativePath, "-c", "Release", "--output"},
		runArgs.Args[:5],
	)
}
