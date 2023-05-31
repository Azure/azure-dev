package cli_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/stretchr/testify/require"
)

// test for errors when running deploy in invalid working directories
func Test_CLI_Deploy_Err_WorkingDirectory(t *testing.T) {
	// running this test in parallel is ok as it uses a t.TempDir()
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(os.Environ(), "AZURE_LOCATION=eastus2")

	err := copySample(dir, "webapp")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit("testenv"), "init")
	require.NoError(t, err)

	// Otherwise, deploy with 'infrastructure has not been provisioned. Run `azd provision`'
	_, err = cli.RunCommand(ctx, "env", "set", "AZURE_SUBSCRIPTION_ID", cfg.SubscriptionID)
	require.NoError(t, err)

	// cd infra
	err = os.MkdirAll(filepath.Join(dir, "infra"), osutil.PermissionDirectory)
	require.NoError(t, err)
	cli.WorkingDirectory = filepath.Join(dir, "infra")

	result, err := cli.RunCommand(ctx, "deploy")
	require.Error(t, err, "deploy should fail in non-project and non-service directory")
	require.Contains(t, result.Stdout, "current working directory")
}

// test for azd deploy with invalid flag options
func Test_CLI_DeployInvalidFlags(t *testing.T) {
	// running this test in parallel is ok as it uses a t.TempDir()
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	envName := randomEnvName()
	t.Logf("AZURE_ENV_NAME: %s", envName)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(os.Environ(), "AZURE_LOCATION=eastus2")

	err := copySample(dir, "webapp")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	// Otherwise, deploy with 'infrastructure has not been provisioned. Run `azd provision`'
	_, err = cli.RunCommand(ctx, "env", "set", "AZURE_SUBSCRIPTION_ID", cfg.SubscriptionID)
	require.NoError(t, err)

	// invalid service name
	res, err := cli.RunCommand(ctx, "deploy", "badServiceName")
	require.Error(t, err)
	require.Contains(t, res.Stdout, "badServiceName")

	// --service with --all
	res, err = cli.RunCommand(ctx, "deploy", "web", "--all")
	require.Error(t, err)
	require.Contains(t, res.Stdout, "--all")
	require.Contains(t, res.Stdout, "<service>")

	// --from-package with --all
	res, err = cli.RunCommand(ctx, "deploy", "--all", "--from-package", "output")
	require.Error(t, err)
	require.Contains(t, res.Stdout, "--all")
	require.Contains(t, res.Stdout, "--from-package")
}
