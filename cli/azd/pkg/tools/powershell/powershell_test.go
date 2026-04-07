// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package powershell

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_Powershell_Prepare(t *testing.T) {
	emptyCtx := tools.ExecutionContext{}

	t.Run("PwshAvailable", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.MockToolInPath("pwsh", nil)

		ps := NewExecutor(mockContext.CommandRunner)
		err := ps.Prepare(*mockContext.Context, "script.ps1", emptyCtx)

		require.NoError(t, err)
	})

	t.Run("PwshNotAvailableFallbackWindows", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			t.Skip("pwsh fallback to powershell is only for Windows")
		}

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.MockToolInPath(
			"pwsh", fmt.Errorf("pwsh: command not found"),
		)
		mockContext.CommandRunner.MockToolInPath("powershell", nil)

		ps := NewExecutor(mockContext.CommandRunner)
		err := ps.Prepare(*mockContext.Context, "script.ps1", emptyCtx)

		require.NoError(t, err)
	})

	t.Run("NoPowerShellInstalled", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.MockToolInPath(
			"pwsh", errors.New("pwsh: command not found"),
		)
		mockContext.CommandRunner.MockToolInPath(
			"powershell", errors.New("powershell: command not found"),
		)

		ps := NewExecutor(mockContext.CommandRunner)
		err := ps.Prepare(*mockContext.Context, "script.ps1", emptyCtx)

		require.Error(t, err)
		if sugErr, ok := errors.AsType[*internal.ErrorWithSuggestion](err); ok {
			require.Contains(t, sugErr.Suggestion, "powershell/scripting/install")
		}
	})

	t.Run("PrepareInlineScript", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.MockToolInPath("pwsh", nil)

		execCtx := tools.ExecutionContext{
			HookName:     "predeploy",
			InlineScript: "Write-Host 'hello'",
		}
		ps := NewExecutor(mockContext.CommandRunner)
		err := ps.Prepare(*mockContext.Context, "script.ps1", execCtx)
		require.NoError(t, err)

		// Verify temp file was created with correct content.
		pe := ps.(*powershellExecutor)
		require.NotEmpty(t, pe.tempFile)
		content, err := os.ReadFile(pe.tempFile)
		require.NoError(t, err)
		require.Contains(
			t, string(content), "ErrorActionPreference",
		)
		require.Contains(
			t, string(content), "Write-Host 'hello'",
		)

		// Verify execute permission is set (Unix only;
		// Windows does not enforce the execute bit).
		if runtime.GOOS != "windows" {
			info, err := os.Stat(pe.tempFile)
			require.NoError(t, err)
			require.Equal(
				t,
				osutil.PermissionExecutableFile,
				info.Mode().Perm(),
			)
		}

		// Cleanup should remove the file.
		require.NoError(t, ps.Cleanup(*mockContext.Context))
		_, err = os.Stat(pe.tempFile)
		require.True(t, os.IsNotExist(err))
	})
}

func Test_Powershell_Execute(t *testing.T) {
	workingDir := "cwd"
	scriptPath := "path/script.ps1"
	env := []string{
		"a=apple",
		"b=banana",
	}

	t.Run("Success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.MockToolInPath("pwsh", nil)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return args.Cmd == "pwsh"
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			require.Equal(t, "pwsh", args.Cmd)
			require.Equal(t, workingDir, args.Cwd)
			require.Equal(t, scriptPath, args.Args[0])
			require.Equal(t, env, args.Env)

			return exec.NewRunResult(0, "", ""), nil
		})

		ps := NewExecutor(mockContext.CommandRunner)
		execCtx := tools.ExecutionContext{Cwd: workingDir, EnvVars: env}
		require.NoError(t, ps.Prepare(*mockContext.Context, scriptPath, execCtx))

		execCtx.Interactive = new(true)
		runResult, err := ps.Execute(
			*mockContext.Context,
			scriptPath,
			execCtx,
		)

		require.NotNil(t, runResult)
		require.NoError(t, err)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.MockToolInPath("pwsh", nil)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return true
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(1, "", "error message"), errors.New("error message")
		})

		ps := NewExecutor(mockContext.CommandRunner)
		execCtx := tools.ExecutionContext{Cwd: workingDir, EnvVars: env}
		require.NoError(t, ps.Prepare(*mockContext.Context, scriptPath, execCtx))

		execCtx.Interactive = new(true)
		runResult, err := ps.Execute(
			*mockContext.Context,
			scriptPath,
			execCtx,
		)

		require.Equal(t, 1, runResult.ExitCode)
		require.Error(t, err)
	})

	tests := []struct {
		name  string
		value tools.ExecutionContext
	}{
		{name: "Interactive", value: tools.ExecutionContext{
			Cwd: workingDir, EnvVars: env, Interactive: new(true),
		}},
		{name: "NonInteractive", value: tools.ExecutionContext{
			Cwd: workingDir, EnvVars: env, Interactive: new(false),
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(context.Background())
			mockContext.CommandRunner.MockToolInPath("pwsh", nil)

			mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
				return true
			}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				require.Equal(t, *test.value.Interactive, args.Interactive)
				return exec.NewRunResult(0, "", ""), nil
			})

			ps := NewExecutor(mockContext.CommandRunner)
			execCtx := tools.ExecutionContext{Cwd: workingDir, EnvVars: env}
			require.NoError(t, ps.Prepare(*mockContext.Context, scriptPath, execCtx))

			runResult, err := ps.Execute(*mockContext.Context, scriptPath, test.value)

			require.NotNil(t, runResult)
			require.NoError(t, err)
		})
	}
}
