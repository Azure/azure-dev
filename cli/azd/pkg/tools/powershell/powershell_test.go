// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package powershell

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_Powershell_Execute(t *testing.T) {
	workingDir := "cwd"
	scriptPath := "path/script.ps1"
	env := []string{
		"a=apple",
		"b=banana",
	}

	t.Run("Success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		// #nosec G101
		userPwsh := "pwsh -NoProfile"
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(args.Cmd, userPwsh)
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			require.Equal(t, userPwsh, args.Cmd)
			require.Equal(t, workingDir, args.Cwd)
			require.Equal(t, scriptPath, args.Args[0])
			require.Equal(t, env, args.Env)

			return exec.NewRunResult(0, "", ""), nil
		})

		PowershellScript := NewPowershellScriptWithMockCheckPath(mockContext.CommandRunner, workingDir, env, func(options tools.ExecOptions) error {
			return nil
		})
		runResult, err := PowershellScript.Execute(
			*mockContext.Context,
			scriptPath,
			tools.ExecOptions{UserPwsh: userPwsh, Interactive: to.Ptr(true)},
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

		PowershellScript := NewPowershellScriptWithMockCheckPath(mockContext.CommandRunner, workingDir, env, func(options tools.ExecOptions) error {
			return nil
		})
		runResult, err := PowershellScript.Execute(
			*mockContext.Context,
			scriptPath,
			tools.ExecOptions{Interactive: to.Ptr(true)},
		)

		require.Equal(t, 1, runResult.ExitCode)
		require.Error(t, err)
	})

	t.Run("NoPowerShellInstalled", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		PowershellScript := NewPowershellScript(mockContext.CommandRunner, workingDir, env)
		_, err := PowershellScript.Execute(
			*mockContext.Context,
			scriptPath,
			tools.ExecOptions{Interactive: to.Ptr(true)},
		)

		require.Error(t, err)
	})

	tests := []struct {
		name  string
		value tools.ExecOptions
	}{
		{name: "Interactive", value: tools.ExecOptions{Interactive: to.Ptr(true)}},
		{name: "NonInteractive", value: tools.ExecOptions{Interactive: to.Ptr(false)}},
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

			PowershellScript := NewPowershellScriptWithMockCheckPath(mockContext.CommandRunner, workingDir, env, func(options tools.ExecOptions) error {
				return nil
			})
			runResult, err := PowershellScript.Execute(*mockContext.Context, scriptPath, test.value)

			require.NotNil(t, runResult)
			require.NoError(t, err)
		})
	}
}
