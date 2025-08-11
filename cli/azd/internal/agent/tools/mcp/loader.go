// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	_ "embed"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

//go:embed mcp.json
var _mcpJson string

// McpConfig represents the overall MCP configuration structure
type McpConfig struct {
	// Servers maps server names to their configurations
	Servers map[string]ServerConfig `json:"servers"`
}

// ServerConfig represents an individual server configuration
type ServerConfig struct {
	// Type specifies the type of MCP server (e.g., "stdio")
	Type string `json:"type"`
	// Command is the executable path or command to run the MCP server
	Command string `json:"command"`
	// Args are optional command-line arguments for the server command
	Args []string `json:"args,omitempty"`
	// Env are optional environment variables for the server process
	Env []string `json:"env,omitempty"`
}

// McpToolsLoader manages the loading of tools from MCP (Model Context Protocol) servers
type McpToolsLoader struct {
	// samplingHandler handles sampling requests from MCP clients
	samplingHandler client.SamplingHandler
}

// NewMcpToolsLoader creates a new instance of McpToolsLoader with the provided sampling handler
func NewMcpToolsLoader(samplingHandler client.SamplingHandler) *McpToolsLoader {
	return &McpToolsLoader{
		samplingHandler: samplingHandler,
	}
}

// LoadTools loads and returns all available tools from configured MCP servers.
// It parses the embedded mcp.json configuration, connects to each server,
// and collects all tools from each successfully connected server.
// Returns an error if the configuration cannot be parsed, but continues
// processing other servers if individual server connections fail.
func (l *McpToolsLoader) LoadTools() ([]common.AnnotatedTool, error) {
	// Deserialize the embedded mcp.json configuration
	var config McpConfig
	if err := json.Unmarshal([]byte(_mcpJson), &config); err != nil {
		return nil, fmt.Errorf("failed to parse mcp.json: %w", err)
	}

	var allTools []common.AnnotatedTool

	// Iterate through each server configuration
	for serverName, serverConfig := range config.Servers {
		// Create MCP client for the server using stdio
		stdioTransport := transport.NewStdio(serverConfig.Command, serverConfig.Env, serverConfig.Args...)
		mcpClient := client.NewClient(stdioTransport, client.WithSamplingHandler(l.samplingHandler))

		ctx := context.Background()

		if err := mcpClient.Start(ctx); err != nil {
			log.Printf("Failed to start MCP client for server %s: %v", serverName, err)
			continue
		}

		// Get tools directly from MCP client
		toolsRequest := mcp.ListToolsRequest{}
		toolsResult, err := mcpClient.ListTools(ctx, toolsRequest)
		if err != nil {
			log.Printf("Failed to list tools from server %s: %v", serverName, err)
			continue
		}

		// Convert MCP tools to langchaingo tools using our adapter
		for _, mcpTool := range toolsResult.Tools {
			toolAdapter := NewMcpToolAdapter(serverName, mcpTool, mcpClient)
			allTools = append(allTools, toolAdapter)
		}
	}

	return allTools, nil
}
