// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mcp

import "github.com/mark3labs/mcp-go/client"

// McpConfig represents the overall MCP configuration structure
type McpConfig struct {
	// Servers maps server names to their configurations
	Servers map[string]*ServerConfig `json:"servers"`
}

type Capabilities struct {
	Sampling    client.SamplingHandler
	Elicitation client.ElicitationHandler
}

// ServerConfig represents an individual server configuration
type ServerConfig struct {
	// Type specifies the type of MCP server (e.g., "stdio", "http")
	Type string `json:"type"`
	// Url is the HTTP url of the MCP server
	Url string `json:"url"`
	// Command is the executable path or command to run the MCP server
	Command string `json:"command"`
	// Args are optional command-line arguments for the server command
	Args []string `json:"args,omitempty"`
	// Env are optional environment variables for the server process
	Env []string `json:"env,omitempty"`
}
