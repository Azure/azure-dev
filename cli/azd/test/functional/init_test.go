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
	"github.com/azure/azure-dev/cli/azd/test/gitcli"
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

func Test_CLI_Init_From_App(t *testing.T) {
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
	cli.Env = append(cli.Env, "AZURE_DEV_COLLECT_TELEMETRY=no")

	err = copySample(appDir, "py-postgres")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(
		ctx,
		"Use code in the current directory\n"+
			"Confirm and continue initializing my app\n"+
			"appdb\n"+
			"TESTENV\n",
		"init",
	)
	require.NoError(t, err)

	require.FileExists(t, filepath.Join(dir, "infra", "main.bicep"))
	require.FileExists(t, filepath.Join(dir, "azure.yaml"))
	require.FileExists(t, filepath.Join(dir, "infra", "app", "app.bicep"))
	require.FileExists(t, filepath.Join(dir, "infra", "app", "db-postgres.bicep"))
}

func Test_CLI_Init_With_Advanced_Azdignore(t *testing.T) {
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	templateDir := tempDirWithDiagnostics(t)

	// Define the files and directories to create in the template
	files := map[string]string{
		"azure.yaml":       "azure content",
		"infra/main.bicep": "main bicep content",
		".azdignore": `*.log
tmp/*
!tmp/important.txt
*.bak
nested/*/
secret.yaml
exactfile.txt
/level1/level2/file.txt
*.hidden
/foo/*
!.gitignore
**/foo/bar
!/foo/bar.baz
abc/**/def
a?c.txt`,
		"error.log":                 "log content",
		"tmp/ignored.txt":           "should be ignored",
		"tmp/important.txt":         "should not be ignored",
		"backup.bak":                "backup content",
		"nested/dir1/ignored.file":  "should be ignored",
		"nested/dir2/ignored.file":  "should be ignored",
		"nested/dir3/important.txt": "should be ignored",
		"secret.yaml":               "secret content",
		"exactfile.txt":             "exact file match",
		"level1/level2/file.txt":    "specific path match",
		"hidden.hidden":             "hidden file match",
		"foo/ignore.txt":            "foo directory ignored",
		"foo/bar":                   "foo/bar file ignored",
		"foo/bar.baz":               "foo/bar.baz file not ignored",
		"abc/some/def/file.txt":     "nested match with wildcards",
		"acc.txt":                   "single character wildcard match",
		"abc.txt":                   "single character wildcard match",
	}

	// Create the template repository with the specified files
	gitcli.CreateGitRepo(t, ctx, templateDir, files)

	// Log the .azdignore content for debugging
	azdignoreContent, err := os.ReadFile(filepath.Join(templateDir, ".azdignore"))
	require.NoError(t, err)
	t.Logf("Contents of .azdignore:\n%s", string(azdignoreContent))

	projectDir := tempDirWithDiagnostics(t)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = projectDir
	cli.Env = append(os.Environ(), "AZURE_LOCATION=eastus2", "GIT_DISCOVERY_ACROSS_FILESYSTEM=1")

	_, err = cli.RunCommandWithStdIn(
		ctx,
		"TESTENV\n",
		"init",
		"-t",
		templateDir,
		"--debug",
	)
	require.NoError(t, err)

	// Define expected ignored and non-ignored files
	ignoredFiles := []string{
		"error.log", "tmp/ignored.txt", "backup.bak", "nested/dir1/ignored.file",
		"nested/dir2/ignored.file", "nested/dir3/important.txt", "secret.yaml",
		"exactfile.txt", "level1/level2/file.txt", "hidden.hidden", "foo/ignore.txt",
		"foo/bar", "abc/some/def/file.txt", "acc.txt", "abc.txt",
	}

	nonIgnoredFiles := []string{
		"azure.yaml", "infra/main.bicep", "tmp/important.txt",
		"foo/bar.baz",
	}

	// Verify ignored files are not present
	for _, ignoredFile := range ignoredFiles {
		if _, err := os.Stat(filepath.Join(projectDir, ignoredFile)); !os.IsNotExist(err) {
			t.Errorf("file '%s' should not exist", ignoredFile)
		}
	}

	// Verify non-ignored files are present
	for _, nonIgnoredFile := range nonIgnoredFiles {
		require.FileExists(t, filepath.Join(projectDir, nonIgnoredFile))
	}
}

func Test_CLI_Init_With_No_Azdignore(t *testing.T) {
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	templateDir := tempDirWithDiagnostics(t)

	// Define the files and directories to create in the template
	files := map[string]string{
		"azure.yaml":       "azure content",
		"infra/main.bicep": "main bicep content",
		"error.log":        "log content",
		"tmp/ignored.txt":  "should be copied",
		"backup.bak":       "backup content",
		"nested/file.txt":  "nested content",
		"secret.yaml":      "secret content",
	}

	// Create the template repository with the specified files
	gitcli.CreateGitRepo(t, ctx, templateDir, files)

	projectDir := tempDirWithDiagnostics(t)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = projectDir
	cli.Env = append(os.Environ(), "AZURE_LOCATION=eastus2", "GIT_DISCOVERY_ACROSS_FILESYSTEM=1")

	_, err := cli.RunCommandWithStdIn(
		ctx,
		"TESTENV\n",
		"init",
		"-t",
		templateDir,
		"--debug",
	)
	require.NoError(t, err)

	// Define expected files which should all be present since there is no .azdignore
	expectedFiles := []string{
		"azure.yaml", "infra/main.bicep", "error.log",
		"tmp/ignored.txt", "backup.bak", "nested/file.txt", "secret.yaml",
	}

	// Verify all files are present
	for _, file := range expectedFiles {
		require.FileExists(t, filepath.Join(projectDir, file))
	}
}

func Test_CLI_Init_With_Empty_Azdignore(t *testing.T) {
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	templateDir := tempDirWithDiagnostics(t)

	// Define the files and directories to create in the template
	files := map[string]string{
		"azure.yaml":       "azure content",
		"infra/main.bicep": "main bicep content",
		".azdignore":       "",
		"error.log":        "log content",
		"tmp/ignored.txt":  "should be copied",
		"backup.bak":       "backup content",
		"nested/file.txt":  "nested content",
		"secret.yaml":      "secret content",
	}

	// Create the template repository with the specified files
	gitcli.CreateGitRepo(t, ctx, templateDir, files)

	projectDir := tempDirWithDiagnostics(t)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = projectDir
	cli.Env = append(os.Environ(), "AZURE_LOCATION=eastus2", "GIT_DISCOVERY_ACROSS_FILESYSTEM=1")

	_, err := cli.RunCommandWithStdIn(
		ctx,
		"TESTENV\n",
		"init",
		"-t",
		templateDir,
		"--debug",
	)
	require.NoError(t, err)

	// Define expected files which should all be present since .azdignore is empty
	expectedFiles := []string{
		"azure.yaml", "infra/main.bicep", "error.log",
		"tmp/ignored.txt", "backup.bak", "nested/file.txt", "secret.yaml",
	}

	// Verify all files are present
	for _, file := range expectedFiles {
		require.FileExists(t, filepath.Join(projectDir, file))
	}
}
