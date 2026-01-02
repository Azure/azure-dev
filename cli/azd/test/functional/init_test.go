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

// Test_CLI_Init_Minimal_Variations covers the core "init --minimal" and interactive "init" scenarios through
// the "Scan current directory" option.
func Test_CLI_Init_Minimal_Variations(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		stdIn  string
		env    []string // test-specific environment variables
		verify func(t *testing.T, dir string)
	}{
		{
			name:  "minimal flag",
			args:  []string{"init", "--minimal"},
			stdIn: "\n",
		},
		{
			name:  "interactive",
			args:  []string{"init"},
			stdIn: "Scan current directory\nConfirm and continue initializing my app\n\n",
		},
		{
			name:   "minimal flag with env flag",
			args:   []string{"init", "-m", "-e", "TESTENV"},
			stdIn:  "\n",
			verify: verifyEnvInitialized("TESTENV"),
		},
		{
			name:   "interactive with env flag",
			args:   []string{"init", "-e", "TESTENV"},
			stdIn:  "Scan current directory\nConfirm and continue initializing my app\n\n",
			verify: verifyEnvInitialized("TESTENV"),
		},
		{
			name:   "interactive with env var",
			args:   []string{"init"},
			stdIn:  "Scan current directory\nConfirm and continue initializing my app\n\n",
			env:    []string{"AZURE_ENV_NAME=TESTENV"},
			verify: verifyEnvInitialized("TESTENV"),
		},
		{
			name:   "minimal with env var",
			args:   []string{"init", "--minimal"},
			stdIn:  "\n",
			env:    []string{"AZURE_ENV_NAME=TESTENV"},
			verify: verifyEnvInitialized("TESTENV"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := newTestContext(t)
			defer cancel()

			dir := tempDirWithDiagnostics(t)
			cli := azdcli.NewCLI(t)
			cli.WorkingDirectory = dir

			if len(tt.env) > 0 {
				cli.Env = append(os.Environ(), tt.env...)
			}

			_, err := cli.RunCommandWithStdIn(ctx, tt.stdIn, tt.args...)
			require.NoError(t, err)

			proj, err := project.Load(ctx, filepath.Join(dir, azdcontext.ProjectFileName))
			require.NoError(t, err)
			require.Equal(t, filepath.Base(dir), proj.Name)

			require.NoDirExists(t, filepath.Join(dir, "infra"))
			require.FileExists(t, filepath.Join(dir, ".gitignore"))

			gitignoreContent, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
			require.NoError(t, err)
			require.Contains(t, string(gitignoreContent), ".azure\n")

			if tt.verify != nil {
				tt.verify(t, dir)
			}
		})
	}
}

// Verifies init for the minimal template, when infra folder already exists.
func Test_CLI_Init_Minimal_With_Existing_Infra_Variations(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		stdIn string
	}{
		{name: "minimal flag", args: []string{"init", "-m"}, stdIn: "\n"},
		{
			name:  "interactive",
			args:  []string{"init"},
			stdIn: "Scan current directory\nConfirm and continue initializing my app\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := newTestContext(t)
			defer cancel()

			dir := tempDirWithDiagnostics(t)
			cli := azdcli.NewCLI(t)
			cli.WorkingDirectory = dir

			infraDir := filepath.Join(dir, "infra")
			require.NoError(t, os.MkdirAll(infraDir, osutil.PermissionDirectory))

			originalBicep := "param location string = 'eastus2'"
			originalParameters := "{\"parameters\": {\"location\": {\"value\": \"eastus2\"}}}"

			bicepPath := filepath.Join(infraDir, "main.bicep")
			parametersPath := filepath.Join(infraDir, "main.parameters.json")
			require.NoError(t, os.WriteFile(bicepPath, []byte(originalBicep), osutil.PermissionFile))
			require.NoError(t, os.WriteFile(parametersPath, []byte(originalParameters), osutil.PermissionFile))

			_, err := cli.RunCommandWithStdIn(ctx, tt.stdIn, tt.args...)
			require.NoError(t, err)

			proj, err := project.Load(ctx, filepath.Join(dir, azdcontext.ProjectFileName))
			require.NoError(t, err)
			require.Equal(t, filepath.Base(dir), proj.Name)

			// Verify infra files are untouched
			bicep, err := os.ReadFile(filepath.Join(infraDir, "main.bicep"))
			require.NoError(t, err)
			parameters, err := os.ReadFile(filepath.Join(infraDir, "main.parameters.json"))
			require.NoError(t, err)

			require.Equal(t, originalBicep, string(bicep))
			require.Equal(t, originalParameters, string(parameters))
		})
	}
}

// Test_CLI_Init_From_App_With_Infra_Variations consolidates tests for initializing from an app with infra.
func Test_CLI_Init_From_App_With_Infra_Variations(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		setupEnvs func() []string // Use a function to avoid issues with os.Environ() in table setup
	}{
		{
			name: "no env",
			args: []string{"init"},
			setupEnvs: func() []string {
				return []string{"AZURE_LOCATION=eastus2"}
			},
		},
		{
			name: "with env flag",
			args: []string{"init", "--environment", "TESTENV"},
			setupEnvs: func() []string {
				return []string{}
			},
		},
		{
			name: "with env var",
			args: []string{"init"},
			setupEnvs: func() []string {
				return []string{
					"AZURE_LOCATION=eastus2",
					"AZURE_ENV_NAME=TESTENV",
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := newTestContext(t)
			defer cancel()

			dir := tempDirWithDiagnostics(t)
			appDir := filepath.Join(dir, "app")
			require.NoError(t, os.MkdirAll(appDir, osutil.PermissionDirectory))

			cli := azdcli.NewCLI(t)
			cli.WorkingDirectory = dir

			cli.Env = append(os.Environ(), "AZD_CONFIG_DIR="+dir, "AZURE_DEV_COLLECT_TELEMETRY=no")
			cli.Env = append(cli.Env, tt.setupEnvs()...)

			require.NoError(t, copySample(appDir, "py-postgres"), "failed expanding sample")

			_, err := cli.RunCommandWithStdIn(
				ctx,
				"Scan current directory\n"+
					"Confirm and continue initializing my app\n"+
					"appdb\n",
				tt.args...,
			)
			require.NoError(t, err)

			require.NoDirExists(t, filepath.Join(dir, "infra"))
			require.FileExists(t, filepath.Join(dir, "azure.yaml"))

			// Assertions for environment
			if tt.name != "no env" {
				require.DirExists(t, filepath.Join(dir, ".azure"))
				file, err := os.ReadFile(getTestEnvPath(dir, "TESTENV"))
				require.NoError(t, err)
				require.Regexp(t, regexp.MustCompile(`AZURE_ENV_NAME="TESTENV"`+"\n"), string(file))
			}
		})
	}
}

func Test_CLI_Init_WithinExistingProject(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(os.Environ(),
		"AZURE_LOCATION=eastus2")

	_, err := cli.RunCommandWithStdIn(
		ctx,
		"Scan current directory\nConfirm and continue initializing my app\n\n",
		"init",
	)
	require.NoError(t, err)

	err = os.Mkdir(filepath.Join(dir, "nested"), osutil.PermissionDirectory)
	require.NoError(t, err)

	// Verify init within a nested directory. This should end up creating a new project.
	_, err = cli.RunCommandWithStdIn(
		ctx,
		"Scan current directory\nConfirm and continue initializing my app\n\n",
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

	// While `init` uses git behind the scenes to pull a template, we don't want to bring
	// the history over in the new git repository.
	cmdRun := exec.NewCommandRunner(nil)
	cmdRes, err := cmdRun.Run(ctx, exec.NewRunArgs("git", "-C", dir, "log", "--oneline", "-n", "1"))
	require.Error(t, err)
	require.Contains(t, cmdRes.Stderr, "does not have any commits yet")

	// Ensure the project was initialized from the template by checking that a file from the template is present.
	require.FileExists(t, filepath.Join(dir, "README.md"))
}

// Test_CLI_Init_WithCwdAutoCreate tests the automatic directory creation when using -C/--cwd flag.
func Test_CLI_Init_WithCwdAutoCreate(t *testing.T) {
	tests := []struct {
		name   string
		subDir string // subdirectory to create within temp dir (using -C flag)
		args   []string
	}{
		{
			name:   "single level directory",
			subDir: "new-project",
			args:   []string{"init", "-t", "azure-samples/todo-nodejs-mongo", "--no-prompt"},
		},
		{
			name:   "nested directory",
			subDir: "parent/child/project",
			args:   []string{"init", "-t", "azure-samples/todo-nodejs-mongo", "--no-prompt"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := newTestContext(t)
			defer cancel()

			// Create a parent temp directory
			parentDir := tempDirWithDiagnostics(t)
			cli := azdcli.NewCLI(t)
			cli.WorkingDirectory = parentDir

			// Add -C flag with the subdirectory path
			targetDir := filepath.Join(parentDir, tt.subDir)
			args := append([]string{"-C", tt.subDir}, tt.args...)

			// Directory should not exist before running the command
			require.NoDirExists(t, targetDir)

			// Run the command
			// Note: We expect an error because --no-prompt will fail on environment name prompt
			// but the directory creation should succeed before that
			cli.RunCommand(ctx, args...)

			// Verify the directory was created
			require.DirExists(t, targetDir)

			// Verify that the template was initialized in the created directory
			require.FileExists(t, filepath.Join(targetDir, azdcontext.ProjectFileName))
		})
	}
}

// verifyEnvInitialized is a helper function that returns a verification function.
// This avoids duplicating the verification logic in the test table.
func verifyEnvInitialized(envName string) func(t *testing.T, dir string) {
	return func(t *testing.T, dir string) {
		require.DirExists(t, filepath.Join(dir, ".azure"))
		file, err := os.ReadFile(getTestEnvPath(dir, envName))
		require.NoError(t, err)
		require.Regexp(t, regexp.MustCompile(`AZURE_ENV_NAME="`+envName+`"`+"\n"), string(file))
	}
}
