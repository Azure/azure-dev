package bash

import (
	"context"
	"errors"
	"runtime"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
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

		bashScript := NewBashScript(mockContext.CommandRunner, workingDir, env)
		runResult, err := bashScript.Execute(
			*mockContext.Context,
			scriptPath,
			tools.ExecOptions{Interactive: convert.RefOf(true)},
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

		bashScript := NewBashScript(mockContext.CommandRunner, workingDir, env)
		runResult, err := bashScript.Execute(
			*mockContext.Context,
			scriptPath,
			tools.ExecOptions{Interactive: convert.RefOf(true)},
		)

		require.Equal(t, 1, runResult.ExitCode)
		require.Error(t, err)
	})

	tests := []struct {
		name  string
		value tools.ExecOptions
	}{
		{name: "Interactive", value: tools.ExecOptions{Interactive: convert.RefOf(true)}},
		{name: "NonInteractive", value: tools.ExecOptions{Interactive: convert.RefOf(false)}},
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

			bashScript := NewBashScript(mockContext.CommandRunner, workingDir, env)
			runResult, err := bashScript.Execute(*mockContext.Context, scriptPath, test.value)

			require.NotNil(t, runResult)
			require.NoError(t, err)
		})
	}
}
