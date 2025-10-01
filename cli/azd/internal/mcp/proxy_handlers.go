// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ProxySamplingHandler is a proxy handler that forwards sampling requests to an MCP server.
type ProxySamplingHandler struct {
	host   *McpHost
	server *server.MCPServer
}

// NewProxySamplingHandler creates a new ProxySamplingHandler with the given MCP server.
func NewProxySamplingHandler(server *server.MCPServer) client.SamplingHandler {
	return &ProxySamplingHandler{
		server: server,
	}
}

// CreateMessage sends the sampling request to the MCP server and returns the result.
func (p *ProxySamplingHandler) CreateMessage(
	ctx context.Context,
	request mcp.CreateMessageRequest,
) (*mcp.CreateMessageResult, error) {
	sessionContext := p.server.WithContext(ctx, p.host.GetSession())
	return p.server.RequestSampling(sessionContext, request)
}

// ProxyElicitationHandler is a proxy handler that forwards elicitation requests to an MCP server.
type ProxyElicitationHandler struct {
	host   *McpHost
	server *server.MCPServer
}

func NewProxyElicitationHandler(server *server.MCPServer) client.ElicitationHandler {
	return &ProxyElicitationHandler{
		server: server,
	}
}

// Elicit sends the elicitation request to the MCP server and returns the result.
func (p *ProxyElicitationHandler) Elicit(
	ctx context.Context,
	request mcp.ElicitationRequest,
) (*mcp.ElicitationResult, error) {
	sessionContext := p.server.WithContext(ctx, p.host.GetSession())
	return p.server.RequestElicitation(sessionContext, request)
}
