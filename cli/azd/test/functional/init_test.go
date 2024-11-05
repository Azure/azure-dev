// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/stretchr/testify/require"
)

// Verifies init for the minimal template.
// - The project layout is valid (azure.yaml, .azure, infra/)
// - The template creates a valid environment file
func Test_CLI_Init_Minimal(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(os.Environ(), "AZURE_LOCATION=eastus2")

	_, err := cli.RunCommandWithStdIn(
		ctx,
		"Select a template\nMinimal\nTESTENV\n",
		"init",
	)
	require.NoError(t, err)

	file, err := os.ReadFile(getTestEnvPath(dir, "TESTENV"))

	require.NoError(t, err)
	require.Regexp(t, regexp.MustCompile(`AZURE_ENV_NAME="TESTENV"`+"\n"), string(file))

	proj, err := project.Load(ctx, filepath.Join(dir, azdcontext.ProjectFileName))
	require.NoError(t, err)
	require.Equal(t, filepath.Base(dir), proj.Name)

	require.DirExists(t, filepath.Join(dir, ".azure"))
	require.FileExists(t, filepath.Join(dir, "infra", "main.bicep"))
	require.FileExists(t, filepath.Join(dir, "infra", "main.parameters.json"))
}

// Verifies init for the minimal template, when infra folder already exists with main.bicep and main.parameters.json.
func Test_CLI_Init_Minimal_With_Existing_Infra(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(os.Environ(), "AZURE_LOCATION=eastus2")

	err := os.MkdirAll(filepath.Join(dir, "infra"), osutil.PermissionDirectory)
	require.NoError(t, err)

	originalBicep := "param location string = 'eastus2'"
	originalParameters := "{\"parameters\": {\"location\": {\"value\": \"eastus2\"}}}"

	err = os.WriteFile(filepath.Join(dir, "infra", "main.bicep"), []byte(originalBicep), osutil.PermissionFile)
	require.NoError(t, err)

	err = os.WriteFile(
		filepath.Join(dir, "infra", "main.parameters.json"),
		[]byte(originalParameters),
		osutil.PermissionFile)
	require.NoError(t, err)

	_, err = cli.RunCommandWithStdIn(
		ctx,
		"Select a template\n"+
			"y\n"+ // Say yes to initialize in existing folder
			"Minimal\n"+ // Choose minimal
			"TESTENV\n", // Provide environment name
		"init",
	)
	require.NoError(t, err)

	file, err := os.ReadFile(getTestEnvPath(dir, "TESTENV"))

	require.NoError(t, err)
	require.Regexp(t, regexp.MustCompile(`AZURE_ENV_NAME="TESTENV"`+"\n"), string(file))

	proj, err := project.Load(ctx, filepath.Join(dir, azdcontext.ProjectFileName))
	require.NoError(t, err)
	require.Equal(t, filepath.Base(dir), proj.Name)

	require.DirExists(t, filepath.Join(dir, ".azure"))
	bicep, err := os.ReadFile(filepath.Join(dir, "infra", "main.bicep"))
	require.NoError(t, err)

	parameters, err := os.ReadFile(filepath.Join(dir, "infra", "main.parameters.json"))
	require.NoError(t, err)

	require.Equal(t, originalBicep, string(bicep))
	require.Equal(t, originalParameters, string(parameters))

	require.FileExists(t, filepath.Join(dir, "infra", "main.azd.bicep"))
	require.FileExists(t, filepath.Join(dir, "infra", "main.parameters.azd.json"))
}

func Test_CLI_Init_WithinExistingProject(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(os.Environ(), "AZURE_LOCATION=eastus2")

	// Setup: Create a project
	_, err := cli.RunCommandWithStdIn(
		ctx,
		"Select a template\nMinimal\nTESTENV\n",
		"init",
	)
	require.NoError(t, err)

	err = os.Mkdir(filepath.Join(dir, "nested"), osutil.PermissionDirectory)
	require.NoError(t, err)

	// Verify init within a nested directory. This should end up creating a new project.
	_, err = cli.RunCommandWithStdIn(
		ctx,
		"Select a template\nMinimal\nTESTENV\n",
		"init",
		"--cwd",
		"nested",
	)
	require.NoError(t, err)
}

func Test_CLI_Init_CanUseTemplate(t *testing.T) {
	// running this test in parallel is ok as it uses a t.TempDir()
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(os.Environ(), "AZURE_LOCATION=eastus2")

	_, err := cli.RunCommandWithStdIn(
		ctx,
		"TESTENV\n",
		"init",
		"--template",
		"cosmos-dotnet-core-todo-app",
	)
	require.NoError(t, err)

	// While `init` uses git behind the scenes to pull a template, we don't want to bring the history over in the new git
	// repository.
	cmdRun := exec.NewCommandRunner(nil)
	cmdRes, err := cmdRun.Run(ctx, exec.NewRunArgs("git", "-C", dir, "log", "--oneline", "-n", "1"))
	require.Error(t, err)
	require.Contains(t, cmdRes.Stderr, "does not have any commits yet")

	// Ensure the project was initialized from the template by checking that a file from the template is present.
	require.FileExists(t, filepath.Join(dir, "README.md"))
}

func Test_CLI_Init_From_App_With_Infra(t *testing.T) {
	// running this test in parallel is ok as it uses a t.TempDir()
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	appDir := filepath.Join(dir, "app")
	err := os.MkdirAll(appDir, osutil.PermissionDirectory)
	require.NoError(t, err)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(os.Environ(), "AZURE_LOCATION=eastus2")
	cli.Env = append(cli.Env, "AZD_CONFIG_DIR="+dir)
	cli.Env = append(cli.Env, "AZD_ALPHA_ENABLE_COMPOSE=0")
	cli.Env = append(cli.Env, "AZURE_DEV_COLLECT_TELEMETRY=no")

	err = copySample(appDir, "py-postgres")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(
		ctx,
		"Use code in the current directory\n"+
			"Confirm and continue initializing my app\n"+
			"appdb\n"+
			"Use user assigned managed identity\n"+
			"TESTENV\n",
		"init",
	)
	require.NoError(t, err)

	require.FileExists(t, filepath.Join(dir, "infra", "main.bicep"))
	require.FileExists(t, filepath.Join(dir, "infra", "main.parameters.json"))
	require.FileExists(t, filepath.Join(dir, "infra", "resources.bicep"))
	require.FileExists(t, filepath.Join(dir, "azure.yaml"))
}
