// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// MCPTextResult creates a text-content CallToolResult.
func MCPTextResult(format string, args ...interface{}) *mcp.CallToolResult {
	return mcp.NewToolResultText(fmt.Sprintf(format, args...))
}

// MCPJSONResult marshals data to JSON and creates a text-content CallToolResult.
// Returns an error result if marshaling fails.
func MCPJSONResult(data interface{}) *mcp.CallToolResult {
	jsonBytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal JSON: %s", err))
	}

	return mcp.NewToolResultText(string(jsonBytes))
}

// MCPErrorResult creates an error CallToolResult with IsError set to true.
func MCPErrorResult(format string, args ...interface{}) *mcp.CallToolResult {
	return mcp.NewToolResultError(fmt.Sprintf(format, args...))
}
