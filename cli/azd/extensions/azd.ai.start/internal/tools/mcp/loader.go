package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	_ "embed"

	langchaingo_mcp_adapter "github.com/i2y/langchaingo-mcp-adapter"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tmc/langchaingo/callbacks"
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

type McpSamplingHandler struct {
}

func (h *McpSamplingHandler) CreateMessage(ctx context.Context, request mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
	// TODO: implement sampling handler
	return &mcp.CreateMessageResult{}, nil
}

type McpToolsLoader struct {
	callbackHandler callbacks.Handler
}

func NewMcpToolsLoader(callbackHandler callbacks.Handler) *McpToolsLoader {
	return &McpToolsLoader{
		callbackHandler: callbackHandler,
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
		samplingHandler := &McpSamplingHandler{}
		stdioTransport := transport.NewStdio(serverConfig.Command, serverConfig.Env, serverConfig.Args...)
		mcpClient := client.NewClient(stdioTransport, client.WithSamplingHandler(samplingHandler))

		ctx := context.Background()

		if err := mcpClient.Start(ctx); err != nil {
			return nil, err
		}

		// Initialize the connection
		_, err := mcpClient.Initialize(ctx, mcp.InitializeRequest{
			Params: mcp.InitializeParams{
				ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
				ClientInfo: mcp.Implementation{
					Name:    "azd-agent-host",
					Version: "1.0.0",
				},
				Capabilities: mcp.ClientCapabilities{},
			},
		})
		if err != nil {
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
