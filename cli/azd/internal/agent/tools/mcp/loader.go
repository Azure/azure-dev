// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mcp

import (
	"context"
	"log"

	_ "embed"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
	"github.com/azure/azure-dev/cli/azd/internal/mcp"
)

//go:embed mcp.json
var McpJson string

// McpToolsLoader manages the loading of tools from MCP (Model Context Protocol) servers
type McpToolsLoader struct {
	// samplingHandler handles sampling requests from MCP clients
	host *mcp.McpHost
}

// NewMcpToolsLoader creates a new instance of McpToolsLoader with the provided sampling handler
func NewMcpToolsLoader(host *mcp.McpHost) common.ToolLoader {
	return &McpToolsLoader{
		host: host,
	}
}

// LoadTools loads and returns all available tools from configured MCP servers.
// It parses the embedded mcp.json configuration, connects to each server,
// and collects all tools from each successfully connected server.
// Returns an error if the configuration cannot be parsed, but continues
// processing other servers if individual server connections fail.
func (l *McpToolsLoader) LoadTools(ctx context.Context) ([]common.AnnotatedTool, error) {
	allTools := []common.AnnotatedTool{}

	// Convert MCP tools to langchaingo tools using our adapter
	for _, serverName := range l.host.Servers() {
		serverTools, err := l.host.ServerTools(ctx, serverName)
		if err != nil {
			log.Printf("failed to load MCP tools for server %s, %v", serverName, err)
		}

		for _, mcpTool := range serverTools {
			toolAdapter := NewMcpToolAdapter(serverName, mcpTool)
			allTools = append(allTools, toolAdapter)
		}
	}

	return allTools, nil
}
