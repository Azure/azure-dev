// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// McpToolAdapter wraps an MCP tool with full schema fidelity preservation
type McpToolAdapter struct {
	server string
	proxy  server.ServerTool
}

// Ensure McpToolAdapter implements AnnotatedTool interface
var _ common.AnnotatedTool = (*McpToolAdapter)(nil)

// NewMcpToolAdapter creates a new adapter that preserves full MCP tool schema fidelity
func NewMcpToolAdapter(server string, tool server.ServerTool) *McpToolAdapter {
	return &McpToolAdapter{
		server: server,
		proxy:  tool,
	}
}

// Name implements tools.Tool interface
func (m *McpToolAdapter) Name() string {
	return m.proxy.Tool.Name
}

func (m *McpToolAdapter) Server() string {
	return m.server
}

func (m *McpToolAdapter) Description() string {
	return m.proxy.Tool.Description
}

// GetAnnotations returns tool behavior annotations
func (m *McpToolAdapter) Annotations() mcp.ToolAnnotation {
	return m.proxy.Tool.Annotations
}

// Call implements tools.Tool interface
func (m *McpToolAdapter) Call(ctx context.Context, input string) (string, error) {
	// Parse input JSON
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return "", fmt.Errorf("invalid JSON input: %w", err)
	}

	// Create MCP call request
	req := mcp.CallToolRequest{
		Request: mcp.Request{
			Method: "tools/call",
		},
	}
	req.Params.Name = m.proxy.Tool.Name
	req.Params.Arguments = args

	// Call the MCP tool
	result, err := m.proxy.Handler(ctx, req)
	if err != nil {
		return "", fmt.Errorf("MCP tool call failed: %w", err)
	}

	// Handle different content types in result
	if len(result.Content) == 0 {
		return "", fmt.Errorf("empty result from MCP tool")
	}

	// Extract text from various content types
	var response string
	for _, content := range result.Content {
		switch c := content.(type) {
		case mcp.TextContent:
			response += c.Text
		case mcp.ImageContent:
			response += fmt.Sprintf("[Image: %s]", c.Data)
		case mcp.EmbeddedResource:
			if textResource, ok := c.Resource.(mcp.TextResourceContents); ok {
				response += textResource.Text
			} else {
				response += fmt.Sprintf("[Resource: %s]", c.Resource)
			}
		default:
			// Try to marshal unknown content as JSON
			if jsonBytes, err := json.Marshal(content); err == nil {
				response += string(jsonBytes)
			}
		}
	}

	return response, nil
}
