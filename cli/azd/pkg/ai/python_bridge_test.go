package ai

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/python"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_PythonBridge_Init(t *testing.T) {
	tempDir := t.TempDir()
	mockContext := mocks.NewMockContext(context.Background())
	pythonCli := python.NewPythonCli(mockContext.CommandRunner)
	azdCtx := azdcontext.NewAzdContextWithDirectory(tempDir)

	azdConfigDir := filepath.Join(tempDir, ".azd")
	os.Setenv("AZD_CONFIG_DIR", azdConfigDir)

	createdVirtualEnv := false
	installedDependencies := false

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "-m venv .venv")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		createdVirtualEnv = true
		return exec.NewRunResult(0, "", ""), nil
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "-m pip install -r requirements.txt")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		installedDependencies = true
		return exec.NewRunResult(0, "", ""), nil
	})

	userConfigDir, err := config.GetUserConfigDir()
	require.NoError(t, err)

	aiDir := filepath.Join(userConfigDir, "bin", "ai")

	bridge := NewPythonBridge(azdCtx, pythonCli)
	err = bridge.Initialize(*mockContext.Context)
	require.NoError(t, err)
	require.DirExists(t, aiDir)
	require.True(t, createdVirtualEnv)
	require.True(t, installedDependencies)
}

func Test_PythonBridge_Run(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	pythonCli := python.NewPythonCli(mockContext.CommandRunner)
	azdCtx := azdcontext.NewAzdContextWithDirectory(t.TempDir())

	ran := false
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.HasSuffix(command, "script.py arg1 arg2")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		ran = true
		return exec.NewRunResult(0, "result", ""), nil
	})

	bridge := NewPythonBridge(azdCtx, pythonCli)
	result, err := bridge.Run(*mockContext.Context, "script.py", "arg1", "arg2")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, ran)

	require.Equal(t, "result", result.Stdout)
	require.Equal(t, "", result.Stderr)
	require.Equal(t, 0, result.ExitCode)
}
