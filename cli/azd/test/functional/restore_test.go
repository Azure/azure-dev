package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/stretchr/testify/require"
)

// test for errors when running restore in invalid working directories
func Test_CLI_Restore_Err_WorkingDirectory(t *testing.T) {
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	envName := randomEnvName()
	t.Logf("AZURE_ENV_NAME: %s", envName)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)

	err := copySample(dir, "restoreapp")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	for _, service := range restoreAppServices {
		service.RequireNotRestored(t, dir)
	}

	// non-project, non-service directory
	nonProjectServiceDir := filepath.Join(dir, "infra")
	err = os.MkdirAll(nonProjectServiceDir, osutil.PermissionDirectory)
	require.NoError(t, err)
	cli.WorkingDirectory = nonProjectServiceDir

	result, err := cli.RunCommand(ctx, "restore")
	require.Error(t, err, "restore should fail in non-project and non-service directory")
	require.Contains(t, result.Stdout, "current working directory")

	for _, service := range restoreAppServices {
		service.RequireNotRestored(t, dir)
	}

	// sub service directory
	node := restoreAppServices["node"]
	subServiceDir := filepath.Join(dir, node.projectDir, "subDir")
	err = os.MkdirAll(subServiceDir, osutil.PermissionDirectory)
	require.NoError(t, err)
	cli.WorkingDirectory = subServiceDir

	result, err = cli.RunCommand(ctx, "restore")
	require.Error(t, err, "restore should fail in non-project and non-service directory")
	require.Contains(t, result.Stdout, "current working directory")

	for _, service := range restoreAppServices {
		service.RequireNotRestored(t, dir)
	}

	// some other directory without a valid project
	dir = tempDirWithDiagnostics(t)
	t.Logf("EMPTY_DIR: %s", dir)
	cli.WorkingDirectory = dir

	result, err = cli.RunCommand(ctx, "restore")
	require.Error(t, err)
	require.Contains(t, result.Stdout, azdcontext.ErrNoProject.Error())
}

// test restore in a service directory
func Test_CLI_Restore_InServiceDirectory(t *testing.T) {
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

	err := copySample(dir, "restoreapp")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	csharp := restoreAppServices["csharp"]
	csharp.RequireNotRestored(t, dir)

	cli.WorkingDirectory = filepath.Join(dir, csharp.projectDir)
	_, err = cli.RunCommand(ctx, "restore")
	require.NoError(t, err)

	csharp.RequireRestored(t, dir)
}

// test restore using a service name passed explicitly
func Test_CLI_Restore_UsingServiceName(t *testing.T) {
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

	err := copySample(dir, "restoreapp")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	csharp := restoreAppServices["csharp"]
	csharp.RequireNotRestored(t, dir)

	_, err = cli.RunCommand(ctx, "restore", csharp.name)
	require.NoError(t, err)

	csharp.RequireRestored(t, dir)
}

// test restore all in the project directory
func Test_CLI_RestoreAll_InProjectDir(t *testing.T) {
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

	err := copySample(dir, "restoreapp")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	for _, service := range restoreAppServices {
		service.RequireNotRestored(t, dir)
	}

	_, err = cli.RunCommand(ctx, "restore")
	require.NoError(t, err)

	for _, service := range restoreAppServices {
		service.RequireRestored(t, dir)
	}
}

// test restore --all
func Test_CLI_RestoreAll_UsingFlags(t *testing.T) {
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

	err := copySample(dir, "restoreapp")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	for _, service := range restoreAppServices {
		service.RequireNotRestored(t, dir)
	}

	nonProjectServiceDir := filepath.Join(dir, "infra")
	err = os.MkdirAll(nonProjectServiceDir, osutil.PermissionDirectory)
	require.NoError(t, err)
	cli.WorkingDirectory = nonProjectServiceDir
	_, err = cli.RunCommand(ctx, "restore", "--all")
	require.NoError(t, err)

	for _, service := range restoreAppServices {
		service.RequireRestored(t, dir)
	}
}

// Contains the name and restore directory of a service that is expected to be restored
// in `restoreapp` sample
type restoreAppService struct {
	// the service name
	name string

	// the service projectDir
	projectDir string

	// the service's restore directory. relative to the service directory.
	restoreDir string
}

func (s *restoreAppService) RequireRestored(t *testing.T, rootDir string) {
	if s.name == "" || s.projectDir == "" || s.restoreDir == "" {
		panic("service name, projectDir, or restoreDir is empty")
	}
	require.DirExists(
		t,
		filepath.Join(rootDir, s.projectDir, s.restoreDir),
		fmt.Sprintf("service %s should be restored", s.name))
}

func (s *restoreAppService) RequireNotRestored(t *testing.T, rootDir string) {
	if s.name == "" || s.projectDir == "" || s.restoreDir == "" {
		panic("service name, projectDir, or restoreDir is empty")
	}
	require.NoDirExists(
		t,
		filepath.Join(rootDir, s.projectDir, s.restoreDir),
		fmt.Sprintf("service %s should not be restored", s.name))
}

var restoreAppServices = map[string]restoreAppService{
	"node":      {name: "nodeapptest", projectDir: "nodeapp", restoreDir: "node_modules"},
	"container": {name: "containerapptest", projectDir: "containerapp", restoreDir: "node_modules"},
	"py":        {name: "pyapptest", projectDir: "pyapp", restoreDir: "pyapp_env"},
	"csharp":    {name: "csharpapptest", projectDir: "csharpapp", restoreDir: "obj"},
	"func":      {name: "funcapptest", projectDir: "funcapp", restoreDir: "funcapp_env"},
}
