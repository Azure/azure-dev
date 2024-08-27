package cli_test

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
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

func Test_CLI_Package_ZipIgnore(t *testing.T) {
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	// Create a temporary directory for the project
	dir := tempDirWithDiagnostics(t)

	// Set up the CLI with the appropriate working directory and environment variables
	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(os.Environ(), "AZURE_LOCATION=eastus2")

	// Copy the sample project to the app directory
	err := copySample(dir, "dotignore")
	require.NoError(t, err, "failed expanding sample")

	// Run the init command to initialize the project
	_, err = cli.RunCommandWithStdIn(
		ctx,
		"Use code in the current directory\n"+
			"Confirm and continue initializing my app\n"+
			"appdb\n"+
			"TESTENV\n",
		"init",
	)
	require.NoError(t, err)

	// Define the scenarios to test
	scenarios := []struct {
		name              string
		description       string
		enabled           bool
		rootZipIgnore     string
		service1ZipIgnore string
		expectedFiles     map[string]map[string]bool
	}{
		{
			name: "No zipignore",
			description: "Tests the default behavior when no .zipignore files are present. " +
				"Verifies that common directories like __pycache__, .venv, and node_modules are excluded.",
			enabled: true,
			expectedFiles: map[string]map[string]bool{
				"service1": {
					"testfile.py":                            true,
					"__pycache__/testcache.txt":              false,
					".venv/pyvenv.cfg":                       false,
					"node_modules/some_package/package.json": false,
					"logs/log.txt":                           true,
				},
				"service2": {
					"testfile.js":                            true,
					"__pycache__/testcache.txt":              false,
					".venv/pyvenv.cfg":                       false,
					"node_modules/some_package/package.json": false,
					"logs/log.txt":                           true,
				},
			},
		},
		{
			name: "Root zipignore excluding pycache",
			description: "Tests the behavior when a root .zipignore excludes __pycache__.  " +
				"Verifies that __pycache__ is excluded in both services, but other directories are included.",
			enabled:       true,
			rootZipIgnore: "__pycache__\n",
			expectedFiles: map[string]map[string]bool{
				"service1": {
					"testfile.py":                            true,
					"__pycache__/testcache.txt":              false,
					".venv/pyvenv.cfg":                       true,
					"node_modules/some_package/package.json": true,
					"logs/log.txt":                           true,
				},
				"service2": {
					"testfile.js":                            true,
					"__pycache__/testcache.txt":              false,
					".venv/pyvenv.cfg":                       true,
					"node_modules/some_package/package.json": true,
					"logs/log.txt":                           true,
				},
			},
		},
		{
			name: "Root and Service1 zipignore",
			description: "Tests the behavior when both the root and Service1 have .zipignore files.  " +
				"Verifies that the root .zipignore affects both services, but Service1's .zipignore " +
				"takes precedence for its own files.",
			enabled:           true,
			rootZipIgnore:     "logs/\n",
			service1ZipIgnore: "__pycache__\n",
			expectedFiles: map[string]map[string]bool{
				"service1": {
					"testfile.py":                            true,
					"__pycache__/testcache.txt":              false,
					".venv/pyvenv.cfg":                       true,
					"node_modules/some_package/package.json": true,
					"logs/log.txt":                           false,
				},
				"service2": {
					"testfile.js":                            true,
					"__pycache__/testcache.txt":              true,
					".venv/pyvenv.cfg":                       true,
					"node_modules/some_package/package.json": true,
					"logs/log.txt":                           false,
				},
			},
		},
		{
			name: "Service1 zipignore only",
			description: "Tests the behavior when only Service1 has a .zipignore file. " +
				"Verifies that Service1 follows its .zipignore, while Service2 uses the default behavior.",
			enabled:           true,
			service1ZipIgnore: "__pycache__\n",
			expectedFiles: map[string]map[string]bool{
				"service1": {
					"testfile.py":                            true,
					"__pycache__/testcache.txt":              false,
					".venv/pyvenv.cfg":                       true,
					"node_modules/some_package/package.json": true,
					"logs/log.txt":                           true,
				},
				"service2": {
					"testfile.js":                            true,
					"__pycache__/testcache.txt":              false,
					".venv/pyvenv.cfg":                       false,
					"node_modules/some_package/package.json": false,
					"logs/log.txt":                           true,
				},
			},
		},
	}

	for _, scenario := range scenarios {
		if !scenario.enabled {
			continue
		}

		t.Run(scenario.name, func(t *testing.T) {
			// Print the scenario description
			t.Logf("Scenario: %s - %s", scenario.name, scenario.description)

			// Set up .zipignore files based on the scenario
			if scenario.rootZipIgnore != "" {
				err := os.WriteFile(filepath.Join(dir, ".zipignore"), []byte(scenario.rootZipIgnore), 0600)
				require.NoError(t, err)
			}
			if scenario.service1ZipIgnore != "" {
				err := os.WriteFile(filepath.Join(dir, "src", "service1", ".zipignore"),
					[]byte(scenario.service1ZipIgnore), 0600)
				require.NoError(t, err)
			}

			// Run the package command and specify an output path
			outputDir := filepath.Join(dir, "dist_"+strings.ReplaceAll(scenario.name, " ", "_"))
			err = os.Mkdir(outputDir, 0755) // Ensure the directory exists
			require.NoError(t, err)

			_, err = cli.RunCommand(ctx, "package", "--output-path", outputDir)
			require.NoError(t, err)

			// Verify that the package was created and the output directory exists
			files, err := os.ReadDir(outputDir)
			require.NoError(t, err)
			require.Len(t, files, 2)

			// Check contents of Service1 package
			checkServicePackage(t, outputDir, "service1", scenario.expectedFiles["service1"])

			// Check contents of Service2 package
			checkServicePackage(t, outputDir, "service2", scenario.expectedFiles["service2"])

			// Clean up .zipignore files and generated zip files
			os.RemoveAll(outputDir)
			os.Remove(filepath.Join(dir, ".zipignore"))
			os.Remove(filepath.Join(dir, "src", "service1", ".zipignore"))
		})
	}
}

// Helper function to check service package contents
func checkServicePackage(t *testing.T, distPath, serviceName string, expectedFiles map[string]bool) {
	zipFilePath := findServiceZipFile(t, distPath, serviceName)
	zipReader, err := zip.OpenReader(zipFilePath)
	require.NoError(t, err)
	defer zipReader.Close()

	checkZipContents(t, zipReader, expectedFiles, serviceName)
}

// Helper function to find the zip file by service name
func findServiceZipFile(t *testing.T, distPath, serviceName string) string {
	files, err := os.ReadDir(distPath)
	require.NoError(t, err)

	for _, file := range files {
		if filepath.Ext(file.Name()) == ".zip" && strings.Contains(file.Name(), serviceName) {
			return filepath.Join(distPath, file.Name())
		}
	}

	t.Fatalf("Zip file for service '%s' not found", serviceName)
	return ""
}

// Helper function to check zip contents against expected files
func checkZipContents(t *testing.T, zipReader *zip.ReadCloser, expectedFiles map[string]bool, serviceName string) {
	foundFiles := make(map[string]bool)

	for _, file := range zipReader.File {
		foundFiles[file.Name] = true
	}

	for expectedFile, shouldExist := range expectedFiles {
		if shouldExist {
			if !foundFiles[expectedFile] {
				t.Errorf("[%s] Expected file '%s' to be included in the package but it was not found",
					serviceName, expectedFile)
			}
		} else {
			if foundFiles[expectedFile] {
				t.Errorf("[%s] Expected file '%s' to be excluded from the package but it was found",
					serviceName, expectedFile)
			}
		}
	}
}
