// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package integrationTests

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const testManifestURL = "https://github.com/azure-ai-foundry/foundry-samples/blob/main/samples/microsoft/python/getting-started-agents/hosted-agents/calculator-agent/agent.yaml"

var (
	// Global test state
	testSuite *IntegrationTestSuite
	// Verbose logging flag
	verboseLogging bool
	// Current test context for logging
	currentTestName string
)

// logf logs a message if verbose logging is enabled
func logf(format string, args ...interface{}) {
	if verboseLogging {
		prefix := "[INTEGRATION]"
		if currentTestName != "" {
			prefix = fmt.Sprintf("[%s]", currentTestName)
		}
		log.Printf(prefix+" "+format, args...)
	}
}

// logCommandOutput logs command output if verbose logging is enabled
func logCommandOutput(cmd string, output []byte) {
	if verboseLogging && len(output) > 0 {
		log.Printf("[COMMAND] %s output:\n%s", cmd, string(output))
	}
}

// IntegrationTestSuite holds shared test resources and state
type IntegrationTestSuite struct {
	azdBinary   string
	testEnvDir  string
	cleanupFunc func()
	projectID   string // Retrieved from azd env after provisioning
}

// TestMain provides package-level setup and teardown for integration tests
func TestMain(m *testing.M) {
	// Check if verbose logging is enabled by looking at command line args
	for _, arg := range os.Args {
		if arg == "-v" || arg == "-test.v" || arg == "-test.v=true" {
			verboseLogging = true
			break
		}
	}
	if verboseLogging {
		log.SetFlags(log.LstdFlags | log.Lshortfile)
	}

	currentTestName = "SETUP"
	logf("Starting integration test suite")

	// Setup
	suite, err := setupIntegrationTestSuite()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to setup integration test suite: %v\n", err)
		os.Exit(1)
	}
	testSuite = suite

	// Run tests
	code := m.Run()

	// Cleanup
	currentTestName = "CLEANUP"
	logf("Running cleanup")
	if testSuite != nil && testSuite.cleanupFunc != nil {
		testSuite.cleanupFunc()
	}
	logf("Integration test suite completed")

	os.Exit(code)
}

// setupIntegrationTestSuite initializes shared test resources
func setupIntegrationTestSuite() (*IntegrationTestSuite, error) {
	logf("Setting up integration test suite")
	suite := &IntegrationTestSuite{}

	// Find azd binary
	azdPath, err := findAzdBinary()
	if err != nil {
		return nil, fmt.Errorf("failed to find azd binary: %w", err)
	}
	suite.azdBinary = azdPath
	logf("Found azd binary: %s", azdPath)

	// Verify azd binary works
	if err := suite.verifyAzdBinary(); err != nil {
		return nil, fmt.Errorf("azd binary verification failed: %w", err)
	}

	// Create test environment directory
	testEnvDir, err := os.MkdirTemp("", "azd-ai-agents-test-env-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create test environment directory: %w", err)
	}
	suite.testEnvDir = testEnvDir
	logf("Created test environment: %s", testEnvDir)
	suite.cleanupFunc = func() {
		// Run azd down to clean up Azure resources
		suite.runAzdDown()
		// Remove local test directory
		if err := os.RemoveAll(testEnvDir); err != nil {
			logf("Warning: failed to remove test directory: %v", err)
		}
	}

	// Initialize test environment with azd template
	logf("Initializing Azure environment...")
	if err := suite.initializeTestEnvironment(); err != nil {
		suite.cleanupFunc()
		return nil, fmt.Errorf("failed to initialize test environment: %w", err)
	}
	logf("Azure environment ready (Project ID: %s)", suite.projectID)

	return suite, nil
}

// verifyAzdBinary ensures the azd binary is working and has the ai agent extension
func (s *IntegrationTestSuite) verifyAzdBinary() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Test basic azd functionality
	cmd := exec.CommandContext(ctx, s.azdBinary, "version")
	output, err := cmd.CombinedOutput()
	logCommandOutput("azd version", output)
	if err != nil {
		return fmt.Errorf("azd version command failed: %w", err)
	}

	// Test ai agent extension is available
	cmd = exec.CommandContext(ctx, s.azdBinary, "ai", "agent", "--help")
	output, err = cmd.CombinedOutput()
	logCommandOutput("azd ai agent --help", output)
	if err != nil {
		return fmt.Errorf("azd ai agent extension not available: %w", err)
	}

	return nil
}

// initializeTestEnvironment sets up the test environment with azd template and provisions resources
func (s *IntegrationTestSuite) initializeTestEnvironment() error {
	// Change to test environment directory once
	originalDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	if err := os.Chdir(s.testEnvDir); err != nil {
		return fmt.Errorf("failed to change to test environment directory: %w", err)
	}
	defer os.Chdir(originalDir)

	// Run azd init command
	if err := s.runAzdInit(); err != nil {
		return fmt.Errorf("failed to run azd init: %w", err)
	}

	// Verify the environment was created successfully
	if err := s.verifyTestEnvironment(); err != nil {
		return fmt.Errorf("test environment verification failed: %w", err)
	}

	// Run azd up to provision Azure resources
	if err := s.runAzdUp(); err != nil {
		return fmt.Errorf("failed to run azd up: %w", err)
	}

	// Retrieve project ID from azd environment
	if err := s.retrieveProjectID(); err != nil {
		return fmt.Errorf("failed to retrieve project ID: %w", err)
	}

	return nil
}

// runAzdInit executes the azd init command
func (s *IntegrationTestSuite) runAzdInit() error {
	logf("Running azd init...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	args := []string{
		"init",
		"-t", "Azure-Samples/azd-ai-starter-basic",
		"-e", "trangevi-test",
		"--location", "westus2",
		"--subscription", "827cb315-a120-4b3d-bd80-93f7b3126af2",
		"--no-prompt",
	}

	cmd := exec.CommandContext(ctx, s.azdBinary, args...)
	cmd.Dir = s.testEnvDir

	// Capture output for debugging
	output, err := cmd.CombinedOutput()
	logCommandOutput("azd init", output)
	if err != nil {
		return fmt.Errorf("azd init command failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}

// runAzdUp executes the azd up command
func (s *IntegrationTestSuite) runAzdUp() error {
	logf("Running azd up (provisioning Azure resources)...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	args := []string{"up", "--no-prompt"}

	cmd := exec.CommandContext(ctx, s.azdBinary, args...)
	cmd.Dir = s.testEnvDir

	// Capture output for debugging
	output, err := cmd.CombinedOutput()
	logCommandOutput("azd up", output)
	if err != nil {
		return fmt.Errorf("azd up command failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}

// runAzdDown executes the azd down command to clean up Azure resources
func (s *IntegrationTestSuite) runAzdDown() error {
	logf("Cleaning up Azure resources...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	args := []string{"down", "--force", "--purge", "--no-prompt"}

	cmd := exec.CommandContext(ctx, s.azdBinary, args...)
	cmd.Dir = s.testEnvDir

	// Capture output for debugging but don't fail cleanup on error
	output, err := cmd.CombinedOutput()
	logCommandOutput("azd down", output)
	if err != nil {
		logf("Warning: Azure cleanup failed: %v", err)
		fmt.Printf("Warning: azd down command failed during cleanup: %v\nOutput: %s\n", err, string(output))
	}

	return nil // Always return nil to not block cleanup
}

// retrieveProjectID gets the Azure AI Project ID from azd environment
func (s *IntegrationTestSuite) retrieveProjectID() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	args := []string{"env", "get-value", "AZURE_AI_PROJECT_ID"}

	cmd := exec.CommandContext(ctx, s.azdBinary, args...)
	cmd.Dir = s.testEnvDir

	// Capture output to get the project ID
	output, err := cmd.Output()
	logCommandOutput("azd env get-value", output)
	if err != nil {
		return fmt.Errorf("failed to get AZURE_AI_PROJECT_ID from azd env: %w", err)
	}

	// Trim whitespace and store the project ID
	s.projectID = strings.TrimSpace(string(output))
	if s.projectID == "" {
		return fmt.Errorf("AZURE_AI_PROJECT_ID is empty")
	}

	return nil
}

// verifyTestEnvironment ensures the test environment was set up correctly
func (s *IntegrationTestSuite) verifyTestEnvironment() error {
	// Check for expected files/directories in the test environment
	expectedPaths := []string{
		filepath.Join(s.testEnvDir, "azure.yaml"),
		filepath.Join(s.testEnvDir, ".azure"),
	}

	for _, path := range expectedPaths {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("expected path %s not found: %w", path, err)
		}
	}

	return nil
}

func TestInitCommand_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Ensure test suite is initialized
	require.NotNil(t, testSuite, "Test suite should be initialized")
	currentTestName = "INIT"
	logf("Running integration tests with project ID: %s", testSuite.projectID)

	tests := []struct {
		name        string
		manifestURL string
		targetDir   string
		wantErr     bool
	}{
		{
			name:        "InitWithValidManifest",
			manifestURL: testManifestURL,
			targetDir:   "calculator-agent-test",
			wantErr:     false,
		},
		{
			name:        "InitWithInvalidManifest",
			manifestURL: "https://invalid-url.com/agent.yaml",
			targetDir:   "invalid-agent-test",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			currentTestName = tt.name
			logf("Running test: %s", tt.name)
			// Create temporary directory for test
			tempDir := t.TempDir()
			targetPath := filepath.Join(tempDir, tt.targetDir)
			logf("Test temp dir: %s", tempDir)
			logf("Target path: %s", targetPath)

			// Execute init command
			err := executeInitCommand(context.Background(), tt.manifestURL, targetPath) // Assert results
			if tt.wantErr {
				require.Error(t, err)
				logf("Test completed (expected error)")
				return
			}

			require.NoError(t, err)

			// Verify expected files were created
			verifyInitializedProject(t, targetPath)
			logf("Test completed successfully")
		})
	}
}

// executeInitCommand executes the AI agent init command with the given parameters
func executeInitCommand(ctx context.Context, manifestURL, targetPath string) error {
	// Add timeout to prevent hanging tests
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	// Prepare command arguments
	args := []string{"ai", "agent", "init", "--no-prompt"}

	if manifestURL != "" {
		args = append(args, "--manifest", manifestURL)
	}

	if targetPath != "" {
		args = append(args, "--src", targetPath)
	}

	// Add project-id retrieved from azd environment
	args = append(args, "--project-id", testSuite.projectID)

	logf("Executing command: %s %s", testSuite.azdBinary, strings.Join(args, " "))
	logf("Working directory: %s", testSuite.testEnvDir)
	// Execute the command
	cmd := exec.CommandContext(ctx, testSuite.azdBinary, args...)

	// Run in the test environment directory where the Azure project is already initialized
	cmd.Dir = testSuite.testEnvDir

	// Capture output for debugging
	output, err := cmd.CombinedOutput()
	logCommandOutput("azd ai agent init", output)
	if err != nil {
		logf("Command failed with error: %v", err)
		return &InitError{
			Message: string(output),
			Err:     err,
		}
	}
	logf("Command completed successfully")

	// Debug: Show files in working directory as well
	if files, err := os.ReadDir(testSuite.testEnvDir); err == nil {
		logf("Files in working directory (%s):", testSuite.testEnvDir)
		for _, file := range files {
			logf("  - %s (dir: %v)", file.Name(), file.IsDir())
		}
	} else {
		logf("Could not read working directory: %v", err)
	}

	return nil
}

// findAzdBinary locates the azd binary for testing
func findAzdBinary() (string, error) {
	// First try to find the binary in the extension's bin directory
	extensionRoot := filepath.Join("..", "..", "..")
	localBinary := filepath.Join(extensionRoot, "azureaiagent")
	if os.PathSeparator == '\\' {
		localBinary += ".exe"
	}

	if _, err := os.Stat(localBinary); err == nil {
		return localBinary, nil
	}

	// Fallback to azd in PATH with extension commands
	azdPath, err := exec.LookPath("azd")
	if err != nil {
		return "", &InitError{
			Message: "azd binary not found in PATH and local binary not available",
			Err:     err,
		}
	}

	return azdPath, nil
}

// verifyInitializedProject verifies that the expected files and structure were created
func verifyInitializedProject(t *testing.T, projectPath string) {
	t.Helper()
	logf("Verifying project at path: %s", projectPath)

	// Verify basic project structure exists
	require.DirExists(t, projectPath, "Project directory should exist")

	// List all files in the project directory for debugging
	if files, err := os.ReadDir(projectPath); err == nil {
		logf("Files in target directory:")
		for _, file := range files {
			logf("  - %s (dir: %v)", file.Name(), file.IsDir())
		}
	} else {
		logf("Could not read target directory: %v", err)
	}

	// Verify expected files exist in target directory
	expectedFilesInTarget := []string{
		"agent.yaml",
		"Dockerfile",
		"main.py",
		"requirements.txt",
	}

	for _, file := range expectedFilesInTarget {
		fullPath := filepath.Join(projectPath, file)
		logf("Checking for file: %s", fullPath)
		require.FileExists(t, fullPath, "Expected file %s should exist in target directory", file)

		// Verify file is not empty
		info, err := os.Stat(fullPath)
		require.NoError(t, err)
		require.Greater(t, info.Size(), int64(0), "File %s should not be empty", file)
	}

	// Verify azure.yaml was updated in the test environment directory
	azureYamlPath := filepath.Join(testSuite.testEnvDir, "azure.yaml")
	logf("Checking for azure.yaml in test environment: %s", azureYamlPath)
	require.FileExists(t, azureYamlPath, "azure.yaml should exist in test environment directory")

	// Additional verifications can be added here based on your init command's behavior
	// For example, checking for infra/ directory, src/ directory, etc.
}

// InitError represents an error from the init command
type InitError struct {
	Message string
	Err     error
}

func (e *InitError) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

func (e *InitError) Unwrap() error {
	return e.Err
}
