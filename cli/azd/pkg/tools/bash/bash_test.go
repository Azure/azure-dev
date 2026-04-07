// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bash

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_Bash_Execute(t *testing.T) {
	workingDir := "cwd"
	scriptPath := "path/script.sh"
	env := []string{
		"a=apple",
		"b=banana",
	}

	t.Run("Prepare", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		executor := NewExecutor(mockContext.CommandRunner)
		execCtx := tools.ExecutionContext{Cwd: workingDir, EnvVars: env}
		err := executor.Prepare(*mockContext.Context, scriptPath, execCtx)
		require.NoError(t, err)
	})

	t.Run("PrepareInlineScript", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		executor := NewExecutor(mockContext.CommandRunner)

		execCtx := tools.ExecutionContext{
			Cwd:          workingDir,
			EnvVars:      env,
			HookName:     "predeploy",
			InlineScript: "echo hello",
		}
		err := executor.Prepare(
			*mockContext.Context, scriptPath, execCtx,
		)
		require.NoError(t, err)

		// Verify temp file was created with correct content.
		be := executor.(*bashExecutor)
		require.NotEmpty(t, be.tempFile)
		content, err := os.ReadFile(be.tempFile)
		require.NoError(t, err)
		require.True(
			t,
			strings.HasPrefix(string(content), "#!/bin/sh"),
		)
		require.Contains(t, string(content), "echo hello")

		// Verify execute permission is set (Unix only;
		// Windows does not enforce the execute bit).
		if runtime.GOOS != "windows" {
			info, err := os.Stat(be.tempFile)
			require.NoError(t, err)
			require.Equal(
				t,
				osutil.PermissionExecutableFile,
				info.Mode().Perm(),
			)
		}

		// Cleanup should remove the file.
		require.NoError(t, executor.Cleanup(*mockContext.Context))
		_, err = os.Stat(be.tempFile)
		require.True(t, os.IsNotExist(err))
	})

	t.Run("Success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return true
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			if runtime.GOOS == "windows" {
				require.Equal(t, "bash", args.Cmd)
			} else {
				require.Equal(t, "", args.Cmd)
			}

			require.Equal(t, workingDir, args.Cwd)
			require.Equal(t, scriptPath, args.Args[0])
			require.Equal(t, env, args.Env)

			return exec.NewRunResult(0, "", ""), nil
		})

		executor := NewExecutor(mockContext.CommandRunner)
		execCtx := tools.ExecutionContext{
			Cwd:         workingDir,
			EnvVars:     env,
			Interactive: new(true),
		}
		runResult, err := executor.Execute(
			*mockContext.Context,
			scriptPath,
			execCtx,
		)

		require.NotNil(t, runResult)
		require.NoError(t, err)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return true
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(1, "", "error message"), errors.New("error message")
		})

		executor := NewExecutor(mockContext.CommandRunner)
		execCtx := tools.ExecutionContext{
			Cwd:         workingDir,
			EnvVars:     env,
			Interactive: new(true),
		}
		runResult, err := executor.Execute(
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

			mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
				return true
			}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				require.Equal(t, *test.value.Interactive, args.Interactive)
				return exec.NewRunResult(0, "", ""), nil
			})

			executor := NewExecutor(mockContext.CommandRunner)
			runResult, err := executor.Execute(*mockContext.Context, scriptPath, test.value)

			require.NotNil(t, runResult)
			require.NoError(t, err)
		})
	}
}
