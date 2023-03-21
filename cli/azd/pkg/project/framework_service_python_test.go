// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/python"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/require"
)

func Test_PythonProject_Restore(t *testing.T) {
	var venvArgs exec.RunArgs
	var pipArgs exec.RunArgs

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.
		When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, fmt.Sprintf("%s -m venv", pythonExe()))
		}).
		RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			venvArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

	mockContext.CommandRunner.
		When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, fmt.Sprintf("%s -m pip install", pythonExe()))
		}).
		RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			pipArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

	env := environment.Ephemeral()
	pythonCli := python.NewPythonCli(mockContext.CommandRunner)
	serviceConfig := createTestServiceConfig("./src/api", AppServiceTarget, ServiceLanguagePython)

	pythonProject := NewPythonProject(pythonCli, env)
	restoreTask := pythonProject.Restore(*mockContext.Context, serviceConfig)
	logProgress(restoreTask)

	result, err := restoreTask.Await()
	require.NoError(t, err)
	require.NotNil(t, result)

	require.Equal(t, pythonExe(), venvArgs.Cmd)
	require.Equal(t,
		[]string{"-m", "venv", "api_env"},
		venvArgs.Args,
	)

	require.Equal(t, pythonExe(), pipArgs.Cmd)
	require.Equal(t,
		[]string{"-m", "pip", "install", "-r", "requirements.txt"},
		pipArgs.Args,
	)
}

func Test_PythonProject_Build(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	env := environment.Ephemeral()
	pythonCli := python.NewPythonCli(mockContext.CommandRunner)
	serviceConfig := createTestServiceConfig("./src/api", AppServiceTarget, ServiceLanguagePython)

	pythonProject := NewPythonProject(pythonCli, env)
	buildTask := pythonProject.Build(*mockContext.Context, serviceConfig, nil)
	logProgress(buildTask)

	result, err := buildTask.Await()
	require.NoError(t, err)
	require.NotNil(t, result)
}

func Test_PythonProject_Package(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)
	mockContext := mocks.NewMockContext(context.Background())

	env := environment.Ephemeral()
	pythonCli := python.NewPythonCli(mockContext.CommandRunner)
	serviceConfig := createTestServiceConfig("./src/api", AppServiceTarget, ServiceLanguagePython)
	err := os.MkdirAll(serviceConfig.Path(), osutil.PermissionDirectory)
	require.NoError(t, err)

	pythonProject := NewPythonProject(pythonCli, env)
	packageTask := pythonProject.Package(
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

	_, err = os.Stat(result.PackagePath)
	require.NoError(t, err)
}

func pythonExe() string {
	if runtime.GOOS == "windows" {
		return "py" // https://peps.python.org/pep-0397
	} else {
		return "python3"
	}
}
