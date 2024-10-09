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

/*
Test_CLI_Package_ZipIgnore

This test verifies that the packaging logic correctly handles the inclusion and exclusion of files
based on the presence or absence of `.webappignore` and `.funcignore` files. The following scenarios are covered:

1. Node_App_Service_With_Webappignore
   - Verifies that `node_modules` is included when `.webappignore` is present,
     and that specific files like `logs/log.txt` are excluded as per the rules defined in the `.webappignore` file.

2. Python_App_Service_With_Webappignore
   - Verifies that Python-specific files like `__pycache__` and `.venv` are included when `.webappignore` is present,
     and files like `logs/log.txt` are excluded based on the rules in the `.webappignore`.

3. Python_App_Service_With_Pycache_Excluded
   - Verifies that `__pycache__` is excluded when a `.webappignore` file explicitly contains a rule to exclude it,
     while other directories like `.venv` are included since there is no exclusion rule for them.

4. Function_App_With_Funcignore
   - Verifies that a Function App respects the `.funcignore` file, ensuring that `logs/log.txt` is excluded
     as per the rules defined in `.funcignore`.

5. Node_App_Service_Without_Webappignore
   - Verifies that `node_modules` is excluded when no `.webappignore` is present,
     and that files like `logs/log.txt` are included since no exclusion rules apply without the `.webappignore`.

6. Python_App_Service_Without_Webappignore
   - Verifies that Python-specific files like `__pycache__`
     and `.venv` are excluded by default when no `.webappignore` is present,
     and that files like `logs/log.txt` are included.

7. Function_App_Without_Funcignore
   - Verifies that when no `.funcignore` file is present, no exclusions are applied, and files such as `logs/log.txt`
     are included in the package.

For each scenario, the test simulates the presence or absence of the relevant
	 `.ignore` files and checks the contents of the resulting
     zip package to ensure the correct files are included or excluded as expected.
*/

func Test_CLI_Package_ZipIgnore(t *testing.T) {
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	// Set this to true if you want to print the directory and zip contents for debugging
	printDebug := false

	// Create a temporary directory for the project
	dir := tempDirWithDiagnostics(t)

	// Set up the CLI with the appropriate working directory and environment variables
	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(os.Environ(), "AZURE_LOCATION=eastus2")

	// Copy the sample project to the app directory
	err := copySample(dir, "dotignore")
	require.NoError(t, err, "failed expanding sample")

	// Print directory contents for debugging if printDebug is true
	if printDebug {
		printDirContents(t, "service_node", filepath.Join(dir, "src", "service_node"))
		printDirContents(t, "service_python", filepath.Join(dir, "src", "service_python"))
		printDirContents(t, "service_python_pycache", filepath.Join(dir, "src", "service_python_pycache"))
		printDirContents(t, "service_function", filepath.Join(dir, "src", "service_function"))
	}

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
		name                   string
		description            string
		serviceName            string // This is the actual service name used in the directory
		expectedFiles          map[string]bool
		shouldDeleteIgnoreFile bool // Flag to simulate the absence of .webappignore or .funcignore
	}{
		{
			name: "Node_App_Service_With_Webappignore",
			description: "Verifies that node_modules are included when " +
				".webappignore is present, and logs/log.txt is excluded.",
			serviceName:            "service_node",
			shouldDeleteIgnoreFile: false,
			expectedFiles: map[string]bool{
				"testfile.js":                        true,
				"node_modules/some_package/index.js": true,  // Included because .webappignore is present
				"logs/log.txt":                       false, // Excluded by .webappignore
			},
		},
		{
			name: "Python_App_Service_With_Webappignore",
			description: "Verifies that __pycache__ and .venv are included when " +
				" .webappignore is present, and logs/log.txt is excluded.",
			serviceName:            "service_python",
			shouldDeleteIgnoreFile: false,
			expectedFiles: map[string]bool{
				"testfile.py":               true,
				"__pycache__/testcache.txt": true,  // Included because .webappignore is present
				".venv/pyvenv.cfg":          true,  // Included because .webappignore is present
				"logs/log.txt":              false, // Excluded by .webappignore
			},
		},
		{
			name:                   "Python_App_Service_With_Pycache_Excluded",
			description:            "Verifies that __pycache__ is excluded when .webappignore has a rule to exclude it.",
			serviceName:            "service_python_pycache",
			shouldDeleteIgnoreFile: false,
			expectedFiles: map[string]bool{
				"testfile.py":               true,
				"__pycache__/testcache.txt": false, // Excluded by .webappignore rule
				".venv/pyvenv.cfg":          true,  // Included because no exclusion rule
				"logs/log.txt":              false, // Excluded by .webappignore
			},
		},
		{
			name:                   "Function_App_With_Funcignore",
			description:            "Verifies that logs/log.txt is excluded when .funcignore is present.",
			serviceName:            "service_function",
			shouldDeleteIgnoreFile: false,
			expectedFiles: map[string]bool{
				"testfile.py":               true,
				"__pycache__/testcache.txt": true,
				".venv/pyvenv.cfg":          true,
				"logs/log.txt":              false, // Excluded by .funcignore
			},
		},
		{
			name:                   "Node_App_Service_Without_Webappignore",
			description:            "Verifies that node_modules is excluded when .webappignore is not present.",
			serviceName:            "service_node",
			shouldDeleteIgnoreFile: true,
			expectedFiles: map[string]bool{
				"testfile.js":                        true,
				"node_modules/some_package/index.js": false, // Excluded because no .webappignore
				"logs/log.txt":                       true,  // Included because no .webappignore
			},
		},
		{
			name:                   "Python_App_Service_Without_Webappignore",
			description:            "Verifies that __pycache__ and .venv are excluded when .webappignore is not present.",
			serviceName:            "service_python",
			shouldDeleteIgnoreFile: true,
			expectedFiles: map[string]bool{
				"testfile.py":               true,
				"__pycache__/testcache.txt": false, // Excluded because no .webappignore
				".venv/pyvenv.cfg":          false, // Excluded because no .webappignore
				"logs/log.txt":              true,  // Included because no .webappignore
			},
		},
		{
			name:                   "Function_App_Without_Funcignore",
			description:            "Verifies that logs/log.txt is included when .funcignore is not present.",
			serviceName:            "service_function",
			shouldDeleteIgnoreFile: true,
			expectedFiles: map[string]bool{
				"testfile.py":               true,
				"__pycache__/testcache.txt": false,
				".venv/pyvenv.cfg":          false,
				"logs/log.txt":              true, // Included because no .funcignore
			},
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			// Print the scenario description
			t.Logf("Scenario: %s - %s", scenario.name, scenario.description)

			// If we're simulating the absence of the ignore file, delete it
			if scenario.shouldDeleteIgnoreFile {
				os.Remove(filepath.Join(dir, "src", scenario.serviceName, ".webappignore"))
				os.Remove(filepath.Join(dir, "src", scenario.serviceName, ".funcignore"))
			}

			// Run the package command and specify an output path
			outputDir := filepath.Join(dir, "dist_"+strings.ReplaceAll(scenario.name, " ", "_"))
			err = os.Mkdir(outputDir, 0755) // Ensure the directory exists
			require.NoError(t, err)

			_, err = cli.RunCommand(ctx, "package", "--output-path", outputDir)
			require.NoError(t, err)

			// Check contents of Service package
			checkServicePackage(t, outputDir, scenario.serviceName, scenario.expectedFiles, printDebug)

			// Clean up generated zip files and ignore files
			os.RemoveAll(outputDir)
		})
	}
}

// Helper function to check service package contents
func checkServicePackage(t *testing.T, distPath, serviceName string, expectedFiles map[string]bool, printDebug bool) {
	zipFilePath := findServiceZipFile(t, distPath, serviceName)
	if printDebug {
		printZipContents(t, serviceName, zipFilePath) // Print the contents of the zip file if printDebug is true
	}
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

// Helper function to print directory contents for debugging
func printDirContents(t *testing.T, serviceName, dir string) {
	t.Logf("[%s] Listing directory: %s", serviceName, dir)
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, file := range files {
		t.Logf("[%s] Found: %s", serviceName, file.Name())
		if file.IsDir() {
			printDirContents(t, serviceName,
				filepath.Join(dir, file.Name())) // Recursive call to list sub-directory contents
		}
	}
}

// Helper function to print the contents of a zip file
func printZipContents(t *testing.T, serviceName, zipFilePath string) {
	t.Logf("[%s] Listing contents of zip file: %s", serviceName, zipFilePath)
	zipReader, err := zip.OpenReader(zipFilePath)
	require.NoError(t, err)
	defer zipReader.Close()

	for _, file := range zipReader.File {
		t.Logf("[%s] Found in zip: %s", serviceName, file.Name)
	}
}

// Helper function to check zip contents against expected files
func checkZipContents(t *testing.T, zipReader *zip.ReadCloser, expectedFiles map[string]bool, serviceName string) {
	foundFiles := make(map[string]bool)

	for _, file := range zipReader.File {
		// Normalize the file name to use forward slashes
		normalizedFileName := strings.ReplaceAll(file.Name, "\\", "/")
		foundFiles[normalizedFileName] = true
	}

	for expectedFile, shouldExist := range expectedFiles {
		// Normalize the expected file name to use forward slashes
		normalizedExpectedFile := strings.ReplaceAll(expectedFile, "\\", "/")
		if shouldExist {
			if !foundFiles[normalizedExpectedFile] {
				t.Errorf("[%s] Expected file '%s' to be included in the package but it was not found",
					serviceName, normalizedExpectedFile)
			}
		} else {
			if foundFiles[normalizedExpectedFile] {
				t.Errorf("[%s] Expected file '%s' to be excluded from the package but it was found",
					serviceName, normalizedExpectedFile)
			}
		}
	}
}
