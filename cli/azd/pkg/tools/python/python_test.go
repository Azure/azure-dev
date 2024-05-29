package python

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_Python_Run(t *testing.T) {
	tempDir := t.TempDir()
	mockContext := mocks.NewMockContext(context.Background())

	pyString, err := checkPath()
	require.NoError(t, err)
	require.NotEmpty(t, pyString)

	var runArgs exec.RunArgs

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		runArgs = args
		return strings.Contains(command, pyString)
	}).Respond(exec.NewRunResult(0, "", ""))

	cli := NewPythonCli(mockContext.CommandRunner)

	runResult, err := cli.Run(*mockContext.Context, tempDir, ".venv", "pf_client.py", "arg1", "arg2", "arg3")
	require.NoError(t, err)
	require.NotNil(t, runResult)
	require.NotNil(t, runArgs)
	require.Equal(t, tempDir, runArgs.Cwd)
	require.Len(t, runArgs.Args, 2)
	require.Contains(t, runArgs.Args[0], "activate")
	require.Equal(t, fmt.Sprintf("%s pf_client.py arg1 arg2 arg3", pyString), runArgs.Args[1])
	require.Equal(t, 0, runResult.ExitCode)
}

func Test_Python_InstallRequirements(t *testing.T) {
	tempDir := t.TempDir()
	mockContext := mocks.NewMockContext(context.Background())

	pyString, err := checkPath()
	require.NoError(t, err)
	require.NotEmpty(t, pyString)

	var runArgs exec.RunArgs

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		runArgs = args
		return strings.Contains(command, "requirements.txt")
	}).Respond(exec.NewRunResult(0, "", ""))

	cli := NewPythonCli(mockContext.CommandRunner)

	err = cli.InstallRequirements(*mockContext.Context, tempDir, ".venv", "requirements.txt")
	require.NoError(t, err)
	require.NotNil(t, runArgs)
	require.Equal(t, tempDir, runArgs.Cwd)
	require.Len(t, runArgs.Args, 2)
	require.Contains(t, runArgs.Args[0], "activate")
	require.Equal(t, fmt.Sprintf("%s -m pip install -r requirements.txt", pyString), runArgs.Args[1])
}

func Test_Python_CreateVirtualEnv(t *testing.T) {
	tempDir := t.TempDir()
	mockContext := mocks.NewMockContext(context.Background())

	pyString, err := checkPath()
	require.NoError(t, err)
	require.NotEmpty(t, pyString)

	var runArgs exec.RunArgs

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		runArgs = args
		return strings.Contains(command, "-m venv .venv")
	}).Respond(exec.NewRunResult(0, "", ""))

	cli := NewPythonCli(mockContext.CommandRunner)

	err = cli.CreateVirtualEnv(*mockContext.Context, tempDir, ".venv")
	require.NoError(t, err)
	require.NotNil(t, runArgs)
	require.Equal(t, pyString, runArgs.Cmd)
	require.Equal(t, tempDir, runArgs.Cwd)
	require.Equal(t, []string{"-m", "venv", ".venv"}, runArgs.Args)
}
