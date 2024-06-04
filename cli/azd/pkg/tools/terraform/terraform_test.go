package terraform

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_WithEnv(t *testing.T) {
	ran := false
	expectedEnvVars := []string{"TF_DATA_DIR=MYDIR", "AZURE_AZ_EMULATOR=true"}

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "terraform"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		ran = true
		require.GreaterOrEqual(t, len(args.Env), 2)
		for _, expectedEnvVar := range expectedEnvVars {
			require.Contains(t, args.Env, expectedEnvVar)
		}
		var pathKey, pathValue string
		for _, envV := range args.Env {
			parts := strings.Split(envV, "=")
			pathKey = parts[0]
			if pathKey == "PATH" {
				if len(parts) > 1 {
					pathValue = parts[1]
				}
				break
			}
		}
		// can't match pathValue as it is different depending on the OS.
		// So just check that it is not empty and contains the path to emulate
		require.NotEmpty(t, pathValue)
		require.Contains(t, pathValue, string(filepath.Separator)+"azEmulate")
		return exec.NewRunResult(0, "", ""), nil
	})

	cli := NewTerraformCli(mockContext.CommandRunner)
	cli.SetEnv(expectedEnvVars)

	_, err := cli.Init(*mockContext.Context, "path/to/module")

	require.NoError(t, err)
	require.True(t, ran)
}
