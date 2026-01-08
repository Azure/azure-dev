// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package testUtilities

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var (
	// Verbose logging flag
	verboseLogging bool
	// Current test context for logging
	currentTestName string
)

// InitializeLogging sets up logging configuration for integration tests
func InitializeLogging() {
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
}

// Logf logs a message if verbose logging is enabled
func Logf(format string, args ...interface{}) {
	if verboseLogging {
		prefix := "[INTEGRATION]"
		if currentTestName != "" {
			prefix = fmt.Sprintf("[%s]", currentTestName)
		}
		log.Printf(prefix+" "+format, args...)
	}
}

// LogCommandOutput logs command output if verbose logging is enabled
func LogCommandOutput(cmd string, output []byte) {
	if verboseLogging && len(output) > 0 {
		log.Printf("[COMMAND] %s output:\n%s", cmd, string(output))
	}
}

// SetCurrentTestName sets the current test name for logging context
func SetCurrentTestName(name string) {
	currentTestName = name
}

// ExecuteInitCommandForAgent executes the AI agent init command with the given parameters
func ExecuteInitCommandForAgent(ctx context.Context, manifestURL, targetPath string, testSuite *IntegrationTestSuite) error {
	// Prepare command arguments
	args := []string{"ai", "agent", "init", "--no-prompt"}

	if manifestURL != "" {
		args = append(args, "--manifest", manifestURL)
	}

	if targetPath != "" {
		args = append(args, "--src", targetPath)
	}

	// Add project-id retrieved from azd environment
	if testSuite.ProjectID != "" {
		args = append(args, "--project-id", testSuite.ProjectID)
	}

	_, err := executeAzdCommand(ctx, testSuite, 2*time.Minute, args)
	if err != nil {
		return err
	}

	// Debug: Show files in working directory as well
	if files, err := os.ReadDir(testSuite.AzdProjectDir); err == nil {
		Logf("Files in working directory (%s):", testSuite.AzdProjectDir)
		for _, file := range files {
			Logf("  - %s (dir: %v)", file.Name(), file.IsDir())
		}
	} else {
		Logf("Could not read working directory: %v", err)
	}

	return nil
}

// VerifyInitializedProject verifies that the expected files and structure were created
func VerifyInitializedProject(t *testing.T, testSuite *IntegrationTestSuite, srcDir string, agentName string) {
	t.Helper()
	if srcDir == "" {
		srcDir = filepath.Join(testSuite.AzdProjectDir, "src", agentName)
	} else if !filepath.IsAbs(srcDir) {
		// If srcDir is relative, make it relative to the azd project directory
		srcDir = filepath.Join(testSuite.AzdProjectDir, srcDir)
	}
	Logf("Verifying project at path: %s", srcDir)

	// Verify basic project structure exists
	require.DirExists(t, srcDir, "Project directory should exist")

	// List all files in the project directory for debugging
	if files, err := os.ReadDir(srcDir); err == nil {
		Logf("Files in target directory:")
		for _, file := range files {
			Logf("  - %s (dir: %v)", file.Name(), file.IsDir())
		}
	} else {
		Logf("Could not read target directory: %v", err)
	}

	// Verify expected files exist in target directory
	expectedFilesInTarget := []string{
		"agent.yaml",
		"Dockerfile",
		"main.py",
		"requirements.txt",
	}

	for _, file := range expectedFilesInTarget {
		fullPath := filepath.Join(srcDir, file)
		Logf("Checking for file: %s", fullPath)
		require.FileExists(t, fullPath, "Expected file %s should exist in target directory", file)

		// Verify file is not empty
		info, err := os.Stat(fullPath)
		require.NoError(t, err)
		require.Greater(t, info.Size(), int64(0), "File %s should not be empty", file)
	}

	// Verify azure.yaml was updated in the test environment
	azureYamlPath := filepath.Join(testSuite.AzdProjectDir, "azure.yaml")
	Logf("Checking for azure.yaml in test environment: %s", azureYamlPath)
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

// executeAzdCommand executes an azd command and returns output and agent version if available
func executeAzdCommand(ctx context.Context, testSuite *IntegrationTestSuite, timeout time.Duration, args []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	Logf("Executing command: %s %s", testSuite.AzdBinary, strings.Join(args, " "))
	Logf("Working directory: %s", testSuite.AzdProjectDir)

	// Execute the command
	cmd := exec.CommandContext(ctx, testSuite.AzdBinary, args...)
	cmd.Dir = testSuite.AzdProjectDir

	// Use a strings.Builder to capture output without truncation
	var outputBuilder strings.Builder
	cmd.Stdout = &outputBuilder
	cmd.Stderr = &outputBuilder

	// Run the command
	err := cmd.Run()
	output := outputBuilder.String()

	// Log the full output
	LogCommandOutput(strings.Join(args, " "), []byte(output))

	if err != nil {
		Logf("Command failed with error: %v", err)
		return "", &InitError{
			Message: output,
			Err:     err,
		}
	}
	Logf("Command completed successfully")

	return output, nil
}

func parseOutputForAgentVersion(output string) string {
	// Parse the agent version from the output
	// Look for pattern: "Agent endpoint: .../agents/{agentName}/versions/{version}"
	versionRegex := regexp.MustCompile(`Agent endpoint:.*?/agents/[^/]+/versions/(\d+)`)
	matches := versionRegex.FindStringSubmatch(output)

	var agentVersion string
	if len(matches) > 1 {
		agentVersion = matches[1]
		Logf("Parsed agent version: %s", agentVersion)
	} else {
		Logf("Warning: Could not parse agent version from output")
	}

	return agentVersion
}

// ExecuteUpCommandForAgent executes the AZD up command with the given parameters
// Returns the deployed agent version number if successful
func ExecuteUpCommandForAgent(ctx context.Context, testSuite *IntegrationTestSuite) (string, error) {
	args := []string{"up", "--no-prompt"}

	output, err := executeAzdCommand(ctx, testSuite, 20*time.Minute, args)
	if err != nil {
		return "", err
	}

	agentVersion := parseOutputForAgentVersion(output)

	return agentVersion, nil
}

// ExecuteDeployCommandForAgent executes the AZD deploy command with the given parameters
// Returns the deployed agent version number if successful
func ExecuteDeployCommandForAgent(ctx context.Context, agentName string, testSuite *IntegrationTestSuite) (string, error) {
	// Prepare command arguments
	args := []string{"deploy"}
	if agentName != "" {
		args = append(args, agentName)
	}
	args = append(args, "--no-prompt")

	output, err := executeAzdCommand(ctx, testSuite, 20*time.Minute, args)
	if err != nil {
		return "", err
	}

	agentVersion := parseOutputForAgentVersion(output)

	return agentVersion, nil
}
