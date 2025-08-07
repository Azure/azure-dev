// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	_ "embed"

	langchaingo_mcp_adapter "github.com/i2y/langchaingo-mcp-adapter"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/tmc/langchaingo/tools"
)

//go:embed mcp.json
var _mcpJson string

// McpConfig represents the overall MCP configuration structure
type McpConfig struct {
	Servers map[string]ServerConfig `json:"servers"`
}

// ServerConfig represents an individual server configuration
type ServerConfig struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Env     []string `json:"env,omitempty"`
}

type McpToolsLoader struct {
	samplingHandler client.SamplingHandler
}

func NewMcpToolsLoader(samplingHandler client.SamplingHandler) *McpToolsLoader {
	return &McpToolsLoader{
		samplingHandler: samplingHandler,
	}
}

func (l *McpToolsLoader) LoadTools() ([]tools.Tool, error) {
	// Deserialize the embedded mcp.json configuration
	var config McpConfig
	if err := json.Unmarshal([]byte(_mcpJson), &config); err != nil {
		return nil, fmt.Errorf("failed to parse mcp.json: %w", err)
	}

	var allTools []tools.Tool

	// Iterate through each server configuration
	for serverName, serverConfig := range config.Servers {
		// Create MCP client for the server using stdio
		stdioTransport := transport.NewStdio(serverConfig.Command, serverConfig.Env, serverConfig.Args...)
		mcpClient := client.NewClient(stdioTransport, client.WithSamplingHandler(l.samplingHandler))

		ctx := context.Background()

		if err := mcpClient.Start(ctx); err != nil {
			return nil, err
		}

		// Create the adapter
		adapter, err := langchaingo_mcp_adapter.New(mcpClient)
		if err != nil {
			return nil, fmt.Errorf("failed to create adapter for server %s: %w", serverName, err)
		}

		// Get all tools from MCP server
		mcpTools, err := adapter.Tools()
		if err != nil {
			return nil, fmt.Errorf("failed to get tools from server %s: %w", serverName, err)
		}

		// Add the tools to our collection
		allTools = append(allTools, mcpTools...)
	}

	return allTools, nil
}
