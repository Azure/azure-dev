// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package initTests

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/test/integrationTests/testUtilities"

	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/stretchr/testify/require"
)

const testManifestURL = "https://github.com/azure-ai-foundry/foundry-samples/blob/main/samples/python/hosted-agents/calculator-agent/agent.yaml"

// Shared test suite instance for init tests
var testSuite *testUtilities.IntegrationTestSuite

// TestMain provides package-level setup and teardown for integration tests
func TestMain(m *testing.M) {
	// Initialize logging configuration
	testUtilities.InitializeLogging()

	testUtilities.SetCurrentTestName("SETUP")
	testUtilities.Logf("Starting integration test suite")

	// Setup
	suite, err := testUtilities.SetupTestSuite()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to setup integration test suite: %v\n", err)
		os.Exit(1)
	}
	testSuite = suite

	// Run tests
	code := m.Run()

	// Cleanup
	testUtilities.SetCurrentTestName("CLEANUP")
	testUtilities.Logf("Running cleanup")
	if testSuite != nil && testSuite.CleanupFunc != nil {
		testSuite.CleanupFunc()
	}
	testUtilities.Logf("Integration test suite completed")

	os.Exit(code)
}

func TestInitCommand_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Ensure test suite is initialized
	require.NotNil(t, testSuite, "Test suite should be initialized")
	testUtilities.SetCurrentTestName("INIT")
	testUtilities.Logf("Running integration tests with project ID: %s", testSuite.ProjectID)

	// Create temporary directory for separated src dir test
	tempDir := t.TempDir()

	tests := []struct {
		name        string
		agentName   string
		manifestURL string
		targetDir   string
		wantErr     bool
	}{
		{
			name:        "InitWithValidManifestDefaultSrc",
			agentName:   "CalculatorAgentLG",
			manifestURL: testManifestURL,
			targetDir:   "",
			wantErr:     false,
		},
		{
			name:        "InitWithValidManifestRelativeSrc",
			agentName:   "CalculatorAgentLG",
			manifestURL: testManifestURL,
			targetDir:   filepath.Join("src", "calculator-agent-test"),
			wantErr:     false,
		},
		{
			name:        "InitWithValidManifestExternalSrc",
			agentName:   "CalculatorAgentLG",
			manifestURL: testManifestURL,
			targetDir:   filepath.Join(tempDir, "calculator-agent-test"),
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
			testUtilities.SetCurrentTestName(tt.name)
			testUtilities.Logf("Running test: %s", tt.name)

			cli := azdcli.NewCLI(t)

			// Execute init command
			err := testUtilities.ExecuteInitCommandForAgent(context.Background(), cli, tt.manifestURL, tt.targetDir, testSuite)
			if tt.wantErr {
				require.Error(t, err)
				testUtilities.Logf("Test completed (expected error)")
				return
			}

			require.NoError(t, err)

			// Verify expected files were created
			testUtilities.VerifyInitializedProject(t, testSuite, tt.targetDir, tt.agentName)
			testUtilities.Logf("Test completed successfully")
		})
	}
}
