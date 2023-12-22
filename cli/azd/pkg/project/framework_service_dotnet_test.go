// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/require"
)

func TestBicepOutputsWithDoubleUnderscoresAreConverted(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	var secrets map[string]string

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "dotnet user-secrets set")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		t.Logf("dotnet user-secrets set was called with: %+v", args)

		jsonBytes, err := io.ReadAll(args.StdIn)
		require.NoError(t, err)
		err = json.Unmarshal(jsonBytes, &secrets)
		require.NoError(t, err)

		return exec.NewRunResult(0, "", ""), nil
	})

	serviceConfig := &ServiceConfig{
		Project: &ProjectConfig{
			Path: "/sample/path/for/test",
		},
		RelativePath: "",
	}

	dotNetCli := dotnet.NewDotNetCli(mockContext.CommandRunner)
	dp := NewDotNetProject(dotNetCli, environment.New("test")).(*dotnetProject)

	err := dp.setUserSecretsFromOutputs(*mockContext.Context, serviceConfig, ServiceLifecycleEventArgs{
		Args: map[string]any{
			"bicepOutput": map[string]provisioning.OutputParameter{
				"EXAMPLE_OUTPUT":          {Type: "string", Value: "foo"},
				"EXAMPLE__NESTED__OUTPUT": {Type: "string", Value: "bar"},
			},
		},
	})

	require.NoError(t, err)
	require.Len(t, secrets, 2)

	require.Equal(t, "bar", secrets["EXAMPLE:NESTED:OUTPUT"])
	require.Equal(t, "foo", secrets["EXAMPLE_OUTPUT"])
}

func Test_DotNetProject_Init(t *testing.T) {
	ranUserSecrets := false
	var runArgs exec.RunArgs

	ostest.Chdir(t, t.TempDir())
	err := os.MkdirAll("./src/api", osutil.PermissionDirectory)
	require.NoError(t, err)
	file, err := os.Create("./src/api/test.csproj")
	require.NoError(t, err)
	require.NoError(t, file.Close())

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "dotnet user-secrets init")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "dotnet user-secrets set")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		ranUserSecrets = true
		runArgs = args

		return exec.NewRunResult(0, "", ""), nil
	})

	env := environment.New("test")
	dotNetCli := dotnet.NewDotNetCli(mockContext.CommandRunner)
	serviceConfig := createTestServiceConfig("./src/api/test.csproj", AppServiceTarget, ServiceLanguageDotNet)

	dotnetProject := NewDotNetProject(dotNetCli, env)

	err = dotnetProject.Initialize(*mockContext.Context, serviceConfig)
	require.NoError(t, err)

	eventArgs := ServiceLifecycleEventArgs{
		Project: serviceConfig.Project,
		Service: serviceConfig,
		Args: map[string]any{
			"bicepOutput": map[string]provisioning.OutputParameter{
				"EXAMPLE_OUTPUT": {Type: "string", Value: "value"},
			},
		},
	}

	err = serviceConfig.RaiseEvent(*mockContext.Context, ServiceEventEnvUpdated, eventArgs)
	require.NoError(t, err)
	require.True(t, ranUserSecrets)

	jsonBytes, err := io.ReadAll(runArgs.StdIn)
	require.NoError(t, err)
	require.Contains(t, string(jsonBytes), "EXAMPLE_OUTPUT")
}

func Test_DotNetProject_Restore(t *testing.T) {
	var runArgs exec.RunArgs
	ostest.Chdir(t, t.TempDir())
	err := os.MkdirAll("./src/api", osutil.PermissionDirectory)
	require.NoError(t, err)
	file, err := os.Create("./src/api/test.csproj")
	require.NoError(t, err)
	require.NoError(t, file.Close())

	// add another *proj file to test multiple project files condition
	file2, err := os.Create("./src/api/test2.vbproj")
	require.NoError(t, err)
	require.NoError(t, file2.Close())

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.
		When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "dotnet restore")
		}).
		RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			runArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

	env := environment.New("test")
	dotNetCli := dotnet.NewDotNetCli(mockContext.CommandRunner)
	serviceConfig := createTestServiceConfig("./src/api/test.csproj", AppServiceTarget, ServiceLanguageCsharp)

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
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	var runArgs exec.RunArgs
	err := os.MkdirAll("./src/api", osutil.PermissionDirectory)
	require.NoError(t, err)
	// add only one project file to test only project file condition
	file, err := os.Create("./src/api/test.csproj")
	require.NoError(t, err)
	require.NoError(t, file.Close())

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.
		When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "dotnet build")
		}).
		RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			runArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

	env := environment.New("test")
	dotNetCli := dotnet.NewDotNetCli(mockContext.CommandRunner)
	serviceConfig := createTestServiceConfig("./src/api", AppServiceTarget, ServiceLanguageCsharp)

	buildOutputDir := filepath.Join(serviceConfig.Path(), "bin", "Release", "net8.0")
	err = os.MkdirAll(buildOutputDir, osutil.PermissionDirectory)
	require.NoError(t, err)

	dotnetProject := NewDotNetProject(dotNetCli, env)
	buildTask := dotnetProject.Build(*mockContext.Context, serviceConfig, nil)
	logProgress(buildTask)

	result, err := buildTask.Await()
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "dotnet", runArgs.Cmd)
	require.Equal(t,
		[]string{"build", filepath.Join(serviceConfig.RelativePath, "test.csproj"), "-c", "Release"},
		runArgs.Args,
	)
}

func Test_DotNetProject_Package(t *testing.T) {
	var runArgs exec.RunArgs

	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)
	err := os.MkdirAll("./src/api", osutil.PermissionDirectory)
	require.NoError(t, err)
	file, err := os.Create("./src/api/test.csproj")
	require.NoError(t, err)
	require.NoError(t, file.Close())

	// add another two *proj files to test multiple project files condition
	file2, err := os.Create("./src/api/test2.vbproj")
	require.NoError(t, err)
	require.NoError(t, file2.Close())

	file3, err := os.Create("./src/api/test3.csproj")
	require.NoError(t, err)
	require.NoError(t, file3.Close())

	var packageDest string

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.
		When(func(args exec.RunArgs, command string) bool {
			packageDest = args.Args[5]
			err := os.WriteFile(filepath.Join(packageDest, "test.txt"), nil, osutil.PermissionFile)
			require.NoError(t, err)

			return strings.Contains(command, "dotnet publish")
		}).
		RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			runArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

	env := environment.New("test")
	dotNetCli := dotnet.NewDotNetCli(mockContext.CommandRunner)
	serviceConfig := createTestServiceConfig("./src/api/test3.csproj", AppServiceTarget, ServiceLanguageCsharp)

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
		[]string{"publish",
			serviceConfig.RelativePath,
			"-c",
			"Release",
			"--output",
			packageDest,
		},
		runArgs.Args,
	)
}
