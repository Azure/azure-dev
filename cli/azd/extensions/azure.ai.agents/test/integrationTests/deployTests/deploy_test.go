// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package deployTests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"azureaiagent/test/integrationTests/testUtilities"

	"github.com/stretchr/testify/require"
)

const testManifestURL = "https://github.com/azure-ai-foundry/foundry-samples/blob/main/samples/python/hosted-agents/calculator-agent/agent.yaml"

// Shared test suite instance for deploy tests
var deployTestSuite *testUtilities.IntegrationTestSuite

func TestMain(m *testing.M) {
	// Initialize logging configuration
	testUtilities.InitializeLogging()

	testUtilities.SetCurrentTestName("SETUP")
	testUtilities.Logf("Starting deploy test suite")

	// Setup test suite once for all deploy tests
	suite, err := testUtilities.SetupTestSuite()
	if err != nil {
		testUtilities.Logf("Failed to setup test suite: %v", err)
		os.Exit(1)
	}
	deployTestSuite = suite

	// Run tests
	code := m.Run()

	// Cleanup
	testUtilities.SetCurrentTestName("CLEANUP")
	testUtilities.Logf("Running cleanup")
	if suite.CleanupFunc != nil {
		suite.CleanupFunc()
	}
	testUtilities.Logf("Deploy test suite completed")

	os.Exit(code)
}

func TestDeployCommand_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Ensure test suite is initialized
	require.NotNil(t, deployTestSuite, "Deploy test suite should be initialized")
	testUtilities.SetCurrentTestName("DEPLOY")
	testUtilities.Logf("Running integration tests with project ID: %s", deployTestSuite.ProjectID)

	tests := []struct {
		name        string
		agentName   string
		manifestURL string
		wantErr     bool
	}{
		{
			name:        "DeployWithValidManifest",
			agentName:   "CalculatorAgentLG",
			manifestURL: testManifestURL,
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testUtilities.SetCurrentTestName(tt.name)
			testUtilities.Logf("Running test: %s", tt.name)

			// Execute init command
			err := testUtilities.ExecuteInitCommandForAgent(context.Background(), tt.manifestURL, "", deployTestSuite)

			require.NoError(t, err)

			// Verify expected files were created
			testUtilities.VerifyInitializedProject(t, deployTestSuite, "", tt.agentName)

			// Execute deploy command
			agentVersion, err := testUtilities.ExecuteDeployCommandForAgent(context.Background(), tt.agentName, deployTestSuite)
			if tt.wantErr {
				require.Error(t, err)
				testUtilities.Logf("Test completed (expected error)")
				return
			}

			require.NoError(t, err)
			if agentVersion != "" {
				testUtilities.Logf("Agent deployed with version: %s", agentVersion)
			}

			// Wait for agent service to be fully ready after deployment
			testUtilities.Logf("Waiting 30 seconds for agent service to initialize...")
			time.Sleep(30 * time.Second)

			// Verify deployment was successful
			verifyAgentDeployment(t, tt.agentName, agentVersion)
			testUtilities.Logf("Test completed successfully")
		})
	}
}

// verifyAgentDeployment checks that the agent was deployed successfully by making API calls
func verifyAgentDeployment(t *testing.T, agentName string, agentVersion string) {
	t.Helper()
	testUtilities.Logf("Verifying deployment for %s (version: %s)...", agentName, agentVersion)

	// Get required environment variables from azd environment
	endpoint, err := deployTestSuite.GetAzdEnvValue("AZURE_AI_PROJECT_ENDPOINT")
	require.NoError(t, err, "Failed to get AZURE_AI_PROJECT_ENDPOINT")
	require.NotEmpty(t, endpoint, "AZURE_AI_PROJECT_ENDPOINT should be set")
	testUtilities.Logf("Using endpoint: %s", endpoint)

	apiVersion := getEnvOrDefault("AGENT_API_VERSION", "2025-05-15-preview")
	// Agent version is required - fail if not provided
	require.NotEmpty(t, agentVersion, "Agent version should be parsed from deploy command output")
	testMessage := getEnvOrDefault("AGENT_TEST_MESSAGE", "What is 2 + 2?")

	// Get Azure access token
	token, err := getAzureAccessToken(t)
	require.NoError(t, err, "Failed to get Azure access token")
	testUtilities.Logf("Successfully obtained Azure access token")

	// Step 1: Create a conversation
	conversationID, err := createConversation(t, endpoint, apiVersion, token)
	require.NoError(t, err, "Failed to create conversation")
	testUtilities.Logf("Created conversation with ID: %s", conversationID)

	// Step 2: Get response from agent
	err = testAgentResponse(t, endpoint, apiVersion, token, agentName, agentVersion, testMessage)
	require.NoError(t, err, "Failed to get valid response from agent")

	testUtilities.Logf("Deployment verification completed successfully")
}

// getEnvOrDefault gets an environment variable or returns a default value
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getAzureAccessToken obtains an Azure access token using az cli
func getAzureAccessToken(t *testing.T) (string, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "az", "account", "get-access-token", "--resource", "https://ai.azure.com", "--query", "accessToken", "-o", "tsv")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get access token: %w", err)
	}

	token := strings.TrimSpace(string(output))
	if token == "" {
		return "", fmt.Errorf("access token is empty")
	}

	return token, nil
}

// createConversation creates a new conversation and returns its ID
func createConversation(t *testing.T, endpoint, apiVersion, token string) (string, error) {
	t.Helper()

	conversationURL := fmt.Sprintf("%s/openai/conversations?api-version=%s", endpoint, apiVersion)

	payload := map[string]interface{}{
		"metadata": map[string]string{
			"test_session": "integration_test_agent_response",
		},
	}

	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err, "Failed to marshal conversation payload")

	req, err := http.NewRequest("POST", conversationURL, bytes.NewBuffer(payloadBytes))
	require.NoError(t, err, "Failed to create conversation request")

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("conversation request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to create conversation (status %d): %s", resp.StatusCode, string(body))
	}

	var conversationData map[string]interface{}
	if err := json.Unmarshal(body, &conversationData); err != nil {
		return "", fmt.Errorf("failed to parse conversation response: %w", err)
	}

	conversationID, ok := conversationData["id"].(string)
	if !ok || conversationID == "" {
		return "", fmt.Errorf("conversation ID not found in response")
	}

	return conversationID, nil
}

// testAgentResponse sends a test message to the agent and verifies the response
func testAgentResponse(t *testing.T, endpoint, apiVersion, token, agentName, agentVersion, testMessage string) error {
	t.Helper()

	requestURL := fmt.Sprintf("%s/openai/responses?api-version=%s", endpoint, apiVersion)

	payload := map[string]interface{}{
		"agent": map[string]string{
			"type":    "agent_reference",
			"name":    agentName,
			"version": agentVersion,
		},
		"input": testMessage,
	}

	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err, "Failed to marshal agent request payload")

	testUtilities.Logf("Agent request payload: %s", string(payloadBytes))

	req, err := http.NewRequest("POST", requestURL, bytes.NewBuffer(payloadBytes))
	require.NoError(t, err, "Failed to create agent request")

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Content-Type", "application/json")

	// Increase timeout for agent response - agents can take time to process
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("agent request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to get response from agent (status %d): %s", resp.StatusCode, string(body))
	}

	var responseData map[string]interface{}
	if err := json.Unmarshal(body, &responseData); err != nil {
		return fmt.Errorf("failed to parse agent response: %w", err)
	}

	testUtilities.Logf("Agent response data: %s", string(body))

	// Verify response doesn't contain errors
	if errorData, hasError := responseData["error"]; hasError && errorData != nil {
		return fmt.Errorf("agent response contains error: %v", errorData)
	}

	// Verify response has output
	output, hasOutput := responseData["output"]
	if !hasOutput {
		return fmt.Errorf("response missing 'output' field")
	}

	// Check if output is a string or array and verify it's not empty
	switch v := output.(type) {
	case string:
		if len(v) == 0 {
			return fmt.Errorf("response output string is empty")
		}
		testUtilities.Logf("Agent response output (string): %s", v)
	case []interface{}:
		if len(v) == 0 {
			return fmt.Errorf("response output array is empty")
		}
		testUtilities.Logf("Agent response output (array with %d items)", len(v))
	default:
		testUtilities.Logf("Agent response output type: %T", v)
	}

	testUtilities.Logf("Agent response validation successful")
	return nil
}
