// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mcp

import (
	"context"
	"fmt"
	"log"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// McpHost manages multiple MCP (Model Context Protocol) servers and their tools
type McpHost struct {
	proxyServer  *server.MCPServer
	servers      map[string]*ServerConfig
	capabilities Capabilities
	clients      map[string]*client.Client
	session      server.ClientSession
}

// McpHostOption defines a functional option for configuring the McpHost
type McpHostOption func(*McpHost)

// WithServers configures the MCP host with a set of server configurations
func WithServers(servers map[string]*ServerConfig) McpHostOption {
	return func(h *McpHost) {
		if len(servers) > 0 {
			h.servers = servers
		}
	}
}

// WithServer adds a single server configuration to the MCP host
func WithCapabilities(capabilities Capabilities) McpHostOption {
	return func(h *McpHost) {
		h.capabilities = capabilities

		proxySamplingHandler, ok := h.capabilities.Sampling.(*ProxySamplingHandler)
		if proxySamplingHandler != nil && ok {
			proxySamplingHandler.host = h
		}

		proxyElicitationHandler, ok := h.capabilities.Elicitation.(*ProxyElicitationHandler)
		if proxyElicitationHandler != nil && ok {
			proxyElicitationHandler.host = h
		}
	}
}

// NewMcpHost creates a new McpHost with the provided options
func NewMcpHost(options ...McpHostOption) *McpHost {
	host := &McpHost{
		capabilities: Capabilities{},
		servers:      make(map[string]*ServerConfig),
		clients:      make(map[string]*client.Client),
	}

	for _, opt := range options {
		opt(host)
	}

	return host
}

// Start initializes and starts all configured MCP servers and their clients
func (h *McpHost) Start(ctx context.Context) error {
	// Iterate through each server configuration
	for serverName, serverConfig := range h.servers {
		var serverTransport transport.Interface

		switch serverConfig.Type {
		case "stdio":
			serverTransport = transport.NewStdio(serverConfig.Command, serverConfig.Env, serverConfig.Args...)
		case "http", "":
			httpTransport, err := transport.NewStreamableHTTP(serverConfig.Url)
			if err != nil {
				log.Printf("Failed to create HTTP transport for server %s: %v", serverName, err)
				continue
			}
			serverTransport = httpTransport
		default:
			log.Printf("Unsupported server type '%s' for server %s", serverConfig.Type, serverName)
			continue
		}

		if err := serverTransport.Start(ctx); err != nil {
			log.Printf("Failed to start transport for server %s: %v", serverName, err)
			continue
		}

		clientOptions := []client.ClientOption{}
		if h.capabilities.Sampling != nil {
			clientOptions = append(clientOptions, client.WithSamplingHandler(h.capabilities.Sampling))
		}
		if h.capabilities.Elicitation != nil {
			clientOptions = append(clientOptions, client.WithElicitationHandler(h.capabilities.Elicitation))
		}

		// Create MCP client for the server using the appropriate transport
		mcpClient := client.NewClient(serverTransport, clientOptions...)

		if err := mcpClient.Start(ctx); err != nil {
			log.Printf("Failed to start MCP client for server %s: %v", serverName, err)
			continue
		}

		initRequest := mcp.InitializeRequest{}
		initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
		initRequest.Params.ClientInfo = mcp.Implementation{
			Name:    "Azure Developer CLI (azd)",
			Version: "1.0.1",
		}

		initResult, err := mcpClient.Initialize(ctx, initRequest)
		if err != nil {
			log.Printf("failed to initialize MCP client for server %s: %v", serverName, err)
			continue
		}

		log.Printf("Initialized MCP client for server %s (%s)", initResult.ServerInfo.Name, initResult.ServerInfo.Version)
		h.clients[serverName] = mcpClient
	}

	return nil
}

func (h *McpHost) SetSession(session server.ClientSession) {
	h.session = session
}

func (h *McpHost) SetProxyServer(server *server.MCPServer) {
	h.proxyServer = server
}

// Servers returns the names of all configured MCP servers
func (h *McpHost) Servers() []string {
	var serverNames []string
	for serverName := range h.servers {
		serverNames = append(serverNames, serverName)
	}

	return serverNames
}

func (h *McpHost) AllTools(ctx context.Context) ([]server.ServerTool, error) {
	var allTools []server.ServerTool

	for serverName := range h.servers {
		serverTools, err := h.ServerTools(ctx, serverName)
		if err != nil {
			log.Printf("Failed to get tools from server %s: %v", serverName, err)
			continue
		}

		allTools = append(allTools, serverTools...)
	}

	return allTools, nil
}

// GetServerTools retrieves all tools from all connected MCP servers
func (h *McpHost) ServerTools(ctx context.Context, serverName string) ([]server.ServerTool, error) {
	var serverTools []server.ServerTool

	client, has := h.clients[serverName]
	if !has {
		log.Printf("No MCP client found for server %s", serverName)
		return nil, fmt.Errorf("no MCP client found for server %s", serverName)
	}

	toolsRequest := mcp.ListToolsRequest{}
	toolsResult, err := client.ListTools(ctx, toolsRequest)
	if err != nil {
		log.Printf("Failed to list tools from server %s: %v", serverName, err)
		return nil, fmt.Errorf("failed to list tools from server %s: %w", serverName, err)
	}

	for _, tool := range toolsResult.Tools {
		proxyToolName := fmt.Sprintf("%s_%s", serverName, tool.Name)
		proxyTool := createProxyTool(proxyToolName, tool, client)
		serverTools = append(serverTools, proxyTool)
	}

	return serverTools, nil
}

// Stop stops all MCP clients and their associated servers
func (h *McpHost) Stop() error {
	for serverName, mcpClient := range h.clients {
		if err := mcpClient.Close(); err != nil {
			log.Printf("Failed to stop MCP client for server %s: %v", serverName, err)
		}
	}

	return nil
}

// Hooks returns server hooks for the MCP host to manage client sessions
func (h *McpHost) Hooks() *server.Hooks {
	return &server.Hooks{
		OnRegisterSession: []server.OnRegisterSessionHookFunc{
			func(ctx context.Context, session server.ClientSession) {
				if session != nil {
					h.SetSession(session)
				}
			},
		},
	}
}

// createExtensionProxyTool creates a proxy tool that forwards calls to the extension's MCP server
func createProxyTool(toolName string, mcpTool mcp.Tool, mcpClient client.MCPClient) server.ServerTool {
	originalToolName := mcpTool.Name
	mcpTool.Name = toolName

	return server.ServerTool{
		Tool: mcpTool,
		Handler: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Forward the tool call to the extension's MCP server
			// Note: We need to use the original tool name when forwarding
			originalRequest := request
			originalRequest.Params.Name = originalToolName

			return mcpClient.CallTool(ctx, originalRequest)
		},
	}
}
