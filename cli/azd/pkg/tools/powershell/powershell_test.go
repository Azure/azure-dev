package powershell

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
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

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return true
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			require.Equal(t, "pwsh", args.Cmd)
			require.Equal(t, workingDir, args.Cwd)
			require.Equal(t, scriptPath, args.Args[0])
			require.Equal(t, env, args.Env)

			return exec.NewRunResult(0, "", ""), nil
		})

		PowershellScript := NewPowershellScript(mockContext.CommandRunner, workingDir, env)
		runResult, err := PowershellScript.Execute(*mockContext.Context, scriptPath, true)

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

		PowershellScript := NewPowershellScript(mockContext.CommandRunner, workingDir, env)
		runResult, err := PowershellScript.Execute(*mockContext.Context, scriptPath, true)

		require.Equal(t, 1, runResult.ExitCode)
		require.Error(t, err)
	})

	tests := []struct {
		name  string
		value bool
	}{
		{name: "Interactive", value: true},
		{name: "NonInteractive", value: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(context.Background())

			mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
				return true
			}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				require.Equal(t, test.value, args.Interactive)
				return exec.NewRunResult(0, "", ""), nil
			})

			PowershellScript := NewPowershellScript(mockContext.CommandRunner, workingDir, env)
			runResult, err := PowershellScript.Execute(*mockContext.Context, scriptPath, test.value)

			require.NotNil(t, runResult)
			require.NoError(t, err)
		})
	}
}
