// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test_CLI_MCP_Server tests that the MCP server starts correctly and can list tools
func Test_CLI_MCP_Server_ListTools(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	// Create MCP client that connects to azd mcp start
	mcpClient, cleanup := createMCPClient(t, ctx)
	defer cleanup()

	// Test listing tools
	result, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	require.NoError(t, err, "Failed to list MCP tools")

	// Verify we have tools available
	assert.Greater(t, len(result.Tools), 0, "Expected at least one MCP tool to be available")

	// Check for some expected tools
	expectedTools := []string{
		"plan_init",
		"architecture_planning",
		"azure_yaml_generation",
		"discovery_analysis",
		"project_validation",
	}

	toolNames := make([]string, len(result.Tools))
	for i, tool := range result.Tools {
		toolNames[i] = tool.Name
	}

	for _, expectedTool := range expectedTools {
		assert.Contains(t, toolNames, expectedTool, "Expected tool %s not found in available tools", expectedTool)
	}

	t.Logf("Found %d MCP tools: %v", len(result.Tools), toolNames)
}

// Test_CLI_MCP_Server_CallTool tests that we can call the plan_init tool
func Test_CLI_MCP_Server_CallTool(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	// Create MCP client that connects to azd mcp start
	mcpClient, cleanup := createMCPClient(t, ctx)
	defer cleanup()

	// Test calling plan_init tool
	toolArgs := map[string]interface{}{
		"query": "Create a simple web application using Node.js and Express",
	}

	callRequest := mcp.CallToolRequest{}
	callRequest.Params.Name = "plan_init"
	callRequest.Params.Arguments = toolArgs

	result, err := mcpClient.CallTool(ctx, callRequest)
	require.NoError(t, err, "Failed to call plan_init tool")

	// Verify the response structure
	assert.NotNil(t, result, "Expected non-nil result from tool call")
	assert.Greater(t, len(result.Content), 0, "Expected tool to return content")

	// Verify we got some meaningful content - just check we have content without checking fields
	hasContent := len(result.Content) > 0
	assert.True(t, hasContent, "Expected tool to return some content")

	t.Logf("Tool call successful, returned %d content items", len(result.Content))
}

// createMCPClient creates an MCP client connected to azd mcp start subprocess
func createMCPClient(t *testing.T, ctx context.Context) (*client.Client, func()) {
	// Use azdcli to get the azd binary path (reuses existing test infrastructure)
	cli := azdcli.NewCLI(t)

	// Create stdio client that will start azd mcp start
	mcpClient, err := client.NewStdioMCPClient(cli.AzdPath, nil, "mcp", "start")
	require.NoError(t, err, "Failed to create MCP client")

	// Start the client (this will start the azd mcp start subprocess)
	err = mcpClient.Start(ctx)
	require.NoError(t, err, "Failed to start MCP client")

	// Initialize the session
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "azd-functional-test",
		Version: "1.0.0",
	}

	initResult, err := mcpClient.Initialize(ctx, initRequest)
	require.NoError(t, err, "Failed to initialize MCP session")
	require.NotNil(t, initResult, "Expected non-nil initialization result")

	cleanup := func() {
		// Close the client connection
		if err := mcpClient.Close(); err != nil {
			t.Logf("Warning: failed to close MCP client: %v", err)
		}
	}

	return mcpClient, cleanup
}
