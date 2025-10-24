// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"archive/zip"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

func Test_CLI_Package_ZipDeploy_Exclusions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		subdir        string
		files         map[string]string
		expectedFiles []string
	}{
		{
			name:   "PythonApp_ExcludesVenvAndCache",
			subdir: "pyapp",
			files: map[string]string{
				".venv/pyvenv.cfg": "py - virtual env",
				"__pycache__/file": "pycache file",
			},
			expectedFiles: []string{"requirements.txt"},
		},
		{
			name:   "PythonApp_WebAppIgnore",
			subdir: "pyapp",
			files: map[string]string{
				// exclude everything except __pycache__
				".webappignore": ".venv/\ntest.log\ntests/**",

				".venv/pyvenv.cfg": "py - virtual env",
				"__pycache__/file": "pycache file",
				"log/test.log":     "some log file",
				"tests/some.py":    "some test file",
			},
			expectedFiles: []string{"requirements.txt", "__pycache__/file"},
		},
		{
			name:   "PythonApp_FuncIgnore",
			subdir: "funcapp",
			files: map[string]string{
				// exclude everything except __pycache__
				".funcignore": ".venv/\ntest.log\ntests/**",

				"local.settings.json":             "local settings -- always ignored",
				"other/other/local.settings.json": "local settings -- always ignored",

				".venv/pyvenv.cfg": "py - virtual env",
				"__pycache__/file": "pycache file",
				"log/test.log":     "some log file",
				"tests/some.py":    "some test file",
			},
			expectedFiles: []string{"requirements.txt", "host.json", "__pycache__/file"},
		},
		{
			name:   "Npm_ExcludeNodeModules",
			subdir: "nodeapp",
			files: map[string]string{
				"node_modules/file": "file.txt",
			},
			expectedFiles: []string{"package.json", "package-lock.json"},
		},
		{
			name:   "Npm_WebAppIgnore",
			subdir: "nodeapp",
			files: map[string]string{
				".webappignore": "node_modules/\ntest.log",

				"log/test.log": "some log file",
				"src/some.js":  "some test file",

				"node_modules/file": "file.txt",
			},
			expectedFiles: []string{"package.json", "package-lock.json", "src/some.js"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := newTestContext(t)
			defer cancel()

			dir := tempDirWithDiagnostics(t)
			t.Logf("DIR: %s", dir)

			err := copySample(dir, "restoreapp")
			require.NoError(t, err, "failed expanding sample")

			cli := azdcli.NewCLI(t)
			cli.Env = append(os.Environ(), "AZURE_LOCATION=eastus2")
			cli.WorkingDirectory = dir

			envName := randomEnvName()
			t.Logf("AZURE_ENV_NAME: %s", envName)

			_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
			require.NoError(t, err)

			appDir := filepath.Join(dir, tc.subdir)
			setupFiles(t, appDir, tc.files)

			cli.WorkingDirectory = appDir
			outputZip := filepath.Join(dir, "dist", tc.name+".zip")
			_, err = cli.RunCommand(ctx, "package", "--output-path", outputZip)
			require.NoError(t, err)

			checkFiles(t, outputZip, tc.expectedFiles)
		})
	}
}

// setupFiles writes files to the filesystem based on the provided map of filepaths and their contents.
// Directories are created automatically if they do not yet exist.
// All files and directories are placed under the provided root.
func setupFiles(t *testing.T, root string, files map[string]string) {
	for path, content := range files {
		fullPath := filepath.Join(root, path)
		err := os.MkdirAll(filepath.Dir(fullPath), 0755)
		require.NoError(t, err)

		err = os.WriteFile(fullPath, []byte(content), 0600)
		require.NoError(t, err)
	}
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
		require.Contains(t, packageResult.Stdout, "Package Output:")
		require.Contains(t, packageResult.Stdout, "dist")

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
		require.Contains(t, packageResult.Stdout, "Package Output:")
		require.Contains(t, packageResult.Stdout, "./dist/web.zip")

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
	require.Contains(t, packageResult.Stdout, "Package Output:")
	require.Contains(t, packageResult.Stdout, os.TempDir())
}

func checkFiles(
	t *testing.T,
	zipFilePath string,
	expectedFiles []string) {
	zipFile, err := os.Open(zipFilePath)
	require.NoError(t, err, "opening zip file")
	defer zipFile.Close()

	// Reopen the zip file for reading
	_, err = zipFile.Seek(0, 0)
	require.NoError(t, err, "failed to seek to start of zip file")
	zipInfo, err := zipFile.Stat()
	require.NoError(t, err, "failed to get zip file info")
	zipReader, err := zip.NewReader(zipFile, zipInfo.Size())
	require.NoError(t, err, "failed to open zip for reading")

	entries := map[string]struct{}{}
	for _, f := range expectedFiles {
		entries[f] = struct{}{}
	}

	for _, zipFile := range zipReader.File {
		_, exists := entries[zipFile.Name]
		if !exists {
			t.Errorf("unexpected file in zip: %s", zipFile.Name)
			continue
		}

		delete(entries, zipFile.Name)
	}

	if len(entries) > 0 {
		t.Errorf("missing files:\n%v", formatFiles(entries))
	}
}

func formatFiles(files map[string]struct{}) string {
	var sb strings.Builder
	keys := slices.Collect(maps.Keys(files))
	slices.Sort(keys)
	for _, path := range keys {
		sb.WriteString(fmt.Sprintf("- %s\n", path))
	}
	return sb.String()
}
