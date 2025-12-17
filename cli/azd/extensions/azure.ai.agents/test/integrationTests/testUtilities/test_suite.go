// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package testUtilities

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// IntegrationTestSuite holds shared test resources and state
type IntegrationTestSuite struct {
	AzdBinary     string
	AzdProjectDir string
	CleanupFunc   func()
	ProjectID     string // Retrieved from azd env after provisioning
}

// SetupTestSuite initializes shared test resources
func SetupTestSuite() (*IntegrationTestSuite, error) {
	Logf("Setting up test suite")
	suite := &IntegrationTestSuite{}

	// Find azd binary
	azdPath, err := findAzdBinary()
	if err != nil {
		return nil, fmt.Errorf("failed to find azd binary: %w", err)
	}
	suite.AzdBinary = azdPath
	Logf("Found azd binary: %s", azdPath)

	// Verify azd binary works
	if err := suite.verifyAzdBinary(); err != nil {
		return nil, fmt.Errorf("azd binary verification failed: %w", err)
	}

	// Create test environment directory
	azdProjectDir, err := os.MkdirTemp("", "azd-ai-agents-test-env-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create test environment directory: %w", err)
	}
	suite.AzdProjectDir = azdProjectDir
	Logf("Created test environment: %s", azdProjectDir)
	suite.CleanupFunc = func() {
		// Run azd down to clean up Azure resources
		suite.runAzdDown()
		// Remove local test directory
		if err := os.RemoveAll(azdProjectDir); err != nil {
			Logf("Warning: failed to remove test directory: %v", err)
		}
	}

	// Initialize test environment with azd template
	Logf("Initializing Azure environment...")
	if err := suite.initializeAzdProject(); err != nil {
		suite.CleanupFunc()
		return nil, fmt.Errorf("failed to initialize test environment: %w", err)
	}
	Logf("Azure environment ready (Project ID: %s)", suite.ProjectID)

	return suite, nil
}

// verifyAzdBinary ensures the azd binary is working and has the ai agent extension
func (s *IntegrationTestSuite) verifyAzdBinary() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Test basic azd functionality
	cmd := exec.CommandContext(ctx, s.AzdBinary, "version")
	var outputBuilder strings.Builder
	cmd.Stdout = &outputBuilder
	cmd.Stderr = &outputBuilder
	err := cmd.Run()
	output := outputBuilder.String()
	LogCommandOutput("azd version", []byte(output))
	if err != nil {
		return fmt.Errorf("azd version command failed: %w", err)
	}

	// Test ai agent extension is available
	cmd = exec.CommandContext(ctx, s.AzdBinary, "ai", "agent", "--help")
	outputBuilder.Reset()
	cmd.Stdout = &outputBuilder
	cmd.Stderr = &outputBuilder
	err = cmd.Run()
	output = outputBuilder.String()
	LogCommandOutput("azd ai agent --help", []byte(output))
	if err != nil {
		return fmt.Errorf("azd ai agent extension not available: %w", err)
	}

	return nil
}

// initializeAzdProject sets up the test environment with azd template and provisions resources
func (s *IntegrationTestSuite) initializeAzdProject() error {
	// Change to test environment directory once
	originalDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	if err := os.Chdir(s.AzdProjectDir); err != nil {
		return fmt.Errorf("failed to change to test environment directory: %w", err)
	}
	defer os.Chdir(originalDir)

	// Run azd init command
	if err := s.runAzdInit(true); err != nil {
		return fmt.Errorf("failed to run azd init: %w", err)
	}

	// Verify the environment was created successfully
	if err := s.verifyTestEnvironment(); err != nil {
		return fmt.Errorf("test environment verification failed: %w", err)
	}

	// Set required environment variable for provisioning an ACR
	if err := s.SetAzdEnvValue("ENABLE_HOSTED_AGENTS", "true"); err != nil {
		return fmt.Errorf("failed to set ENABLE_HOSTED_AGENTS: %w", err)
	}

	// Initialize a calculator agent into the project so we have the model we need
	if err := ExecuteInitCommandForAgent(
		context.Background(),
		nil,
		"https://github.com/azure-ai-foundry/foundry-samples/blob/main/samples/python/hosted-agents/calculator-agent/agent.yaml",
		"", s); err != nil {
		return fmt.Errorf("failed to initialize calculator agent: %w", err)
	}

	// Run azd up to provision Azure resources
	if err := s.runAzdUp(); err != nil {
		return fmt.Errorf("failed to run azd up: %w", err)
	}

	// Retrieve project ID from azd environment
	projectID, err := s.GetAzdEnvValue("AZURE_AI_PROJECT_ID")
	if err != nil {
		return fmt.Errorf("failed to retrieve project ID: %w", err)
	}
	if projectID == "" {
		return fmt.Errorf("AZURE_AI_PROJECT_ID is empty")
	}
	s.ProjectID = projectID

	return nil
}

// runAzdInit executes the azd init command
func (s *IntegrationTestSuite) runAzdInit(withTemplate bool) error {
	Logf("Running azd init...")

	// Generate a unique environment name using a short UUID suffix
	suffix := uuid.New().String()[:8]

	args := []string{
		"init",
		"-e", fmt.Sprintf("azd-extension-integration-tests-%s", suffix),
		"--location", "northcentralus",
		"--subscription", "827cb315-a120-4b3d-bd80-93f7b3126af2",
		"--no-prompt",
	}

	if withTemplate {
		args = append(args, "-t", "Azure-Samples/azd-ai-starter-basic")
	}

	_, err := executeAzdCommandWithExec(context.Background(), s, 5*time.Minute, args)
	if err != nil {
		return fmt.Errorf("azd init command failed: %w", err)
	}

	return nil
}

// runAzdUp executes the azd up command
func (s *IntegrationTestSuite) runAzdUp() error {
	Logf("Running azd up (provisioning Azure resources)...")

	args := []string{"up", "--no-prompt"}

	_, err := executeAzdCommandWithExec(context.Background(), s, 10*time.Minute, args)
	if err != nil {
		return fmt.Errorf("azd up command failed: %w", err)
	}

	return nil
}

// runAzdDown executes the azd down command to clean up Azure resources
func (s *IntegrationTestSuite) runAzdDown() error {
	Logf("Cleaning up Azure resources...")

	args := []string{"down", "--force", "--purge", "--no-prompt"}

	_, err := executeAzdCommandWithExec(context.Background(), s, 10*time.Minute, args)
	if err != nil {
		Logf("Warning: Azure cleanup failed: %v", err)
	}

	return nil // Always return nil to not block cleanup
}

// GetAzdEnvValue retrieves an environment variable value from the azd environment
func (s *IntegrationTestSuite) GetAzdEnvValue(key string) (string, error) {
	Logf("Running azd env get-value %s...", key)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	args := []string{"env", "get-value", key}

	cmd := exec.CommandContext(ctx, s.AzdBinary, args...)
	cmd.Dir = s.AzdProjectDir

	// Capture output to get the value
	output, err := cmd.Output()
	LogCommandOutput(fmt.Sprintf("azd env get-value %s", key), output)
	if err != nil {
		return "", fmt.Errorf("failed to get %s from azd env: %w", key, err)
	}

	// Trim whitespace and return the value
	return strings.TrimSpace(string(output)), nil
}

// SetAzdEnvValue sets an environment variable value in the azd environment
func (s *IntegrationTestSuite) SetAzdEnvValue(key string, value string) error {
	Logf("Running azd env set %s %s...", key, value)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	args := []string{"env", "set", key, value}

	cmd := exec.CommandContext(ctx, s.AzdBinary, args...)
	cmd.Dir = s.AzdProjectDir

	// Capture output to get the value
	output, err := cmd.Output()
	LogCommandOutput(fmt.Sprintf("azd env set %s", key), output)
	if err != nil {
		return fmt.Errorf("failed to set %s in azd env: %w", key, err)
	}

	return nil
}

// verifyTestEnvironment ensures the test environment was set up correctly
func (s *IntegrationTestSuite) verifyTestEnvironment() error {
	// Check for expected files/directories in the test environment
	expectedFiles := []string{
		filepath.Join(s.AzdProjectDir, "azure.yaml"),
		filepath.Join(s.AzdProjectDir, ".azure"),
	}

	for _, path := range expectedFiles {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("expected path %s not found: %w", path, err)
		}
	}

	return nil
}

// findAzdBinary locates the azd binary for testing
func findAzdBinary() (string, error) {
	// First try to find azd in PATH (the main CLI with extension support)
	azdPath, err := exec.LookPath("azd")
	if err == nil {
		return azdPath, nil
	}

	// Fallback to local binary for development scenarios
	extensionRoot := filepath.Join("..", "..", "..")
	localBinary := filepath.Join(extensionRoot, "azureaiagent")
	if os.PathSeparator == '\\' {
		localBinary += ".exe"
	}

	if _, err := os.Stat(localBinary); err == nil {
		return localBinary, nil
	}

	return "", &InitError{
		Message: "azd binary not found in PATH and local binary not available",
		Err:     err,
	}
}
