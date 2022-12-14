package terraform

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_WithEnv(t *testing.T) {
	ran := false
	expectedEnvVars := []string{"TF_DATA_DIR=MYDIR"}

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "terraform"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		ran = true
		require.Len(t, expectedEnvVars, 1)
		require.Equal(t, expectedEnvVars, args.Env)

		return exec.NewRunResult(0, "", ""), nil
	})

	cli := NewTerraformCli(mockContext.CommandRunner)
	cli.SetEnv(expectedEnvVars)

	_, err := cli.Init(*mockContext.Context, "path/to/module")

	require.NoError(t, err)
	require.True(t, ran)
}
