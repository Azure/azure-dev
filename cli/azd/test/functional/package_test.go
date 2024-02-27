package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/stretchr/testify/require"
)

func Test_CLI_Package_Err_WorkingDirectory(t *testing.T) {
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

	// cd infra
	err = os.MkdirAll(filepath.Join(dir, "infra"), osutil.PermissionDirectory)
	require.NoError(t, err)
	cli.WorkingDirectory = filepath.Join(dir, "infra")

	result, err := cli.RunCommand(ctx, "package")
	require.Error(t, err, "package should fail in non-project and non-service directory")
	require.Contains(t, result.Stdout, "current working directory")
}

func Test_CLI_Package_FromServiceDirectory(t *testing.T) {
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

	cli.WorkingDirectory = filepath.Join(dir, "src", "dotnet")

	result, err := cli.RunCommand(ctx, "package")
	require.NoError(t, err)
	require.Contains(t, result.Stdout, "Packaging service web")
}

func Test_CLI_Package_WithOutputPath(t *testing.T) {
	t.Run("AllServices", func(t *testing.T) {
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

		packageResult, err := cli.RunCommand(
			ctx,
			"package", "--output-path", "./dist",
		)
		require.NoError(t, err)
		require.Contains(t, packageResult.Stdout, "Package Output: dist")

		distPath := filepath.Join(dir, "dist")
		files, err := os.ReadDir(distPath)
		require.NoError(t, err)
		require.Len(t, files, 1)
	})

	t.Run("SingleService", func(t *testing.T) {
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

		packageResult, err := cli.RunCommand(
			ctx,
			"package", "web", "--output-path", "./dist/web.zip",
		)
		require.NoError(t, err)
		require.Contains(t, packageResult.Stdout, "Package Output: ./dist/web.zip")

		artifactPath := filepath.Join(dir, "dist", "web.zip")
		info, err := os.Stat(artifactPath)
		require.NoError(t, err)
		require.NotNil(t, info)
	})
}

func Test_CLI_Package(t *testing.T) {
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

	packageResult, err := cli.RunCommand(ctx, "package", "web")
	require.NoError(t, err)
	require.Contains(t, packageResult.Stdout, fmt.Sprintf("Package Output: %s", os.TempDir()))
}
