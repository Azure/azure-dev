// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mcp

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// ProxySamplingHandler is a proxy handler that forwards sampling requests to an MCP server.
type ProxySamplingHandler struct {
	host *McpHost
}

// NewProxySamplingHandler creates a new ProxySamplingHandler with the given MCP server.
func NewProxySamplingHandler() client.SamplingHandler {
	return &ProxySamplingHandler{}
}

// CreateMessage sends the sampling request to the MCP server and returns the result.
func (p *ProxySamplingHandler) CreateMessage(
	ctx context.Context,
	request mcp.CreateMessageRequest,
) (*mcp.CreateMessageResult, error) {
	if err := ensureMcpProxy(p.host); err != nil {
		return nil, err
	}

	sessionContext := p.host.proxyServer.WithContext(ctx, p.host.session)
	return p.host.proxyServer.RequestSampling(sessionContext, request)
}

// ProxyElicitationHandler is a proxy handler that forwards elicitation requests to an MCP server.
type ProxyElicitationHandler struct {
	host *McpHost
}

func NewProxyElicitationHandler() client.ElicitationHandler {
	return &ProxyElicitationHandler{}
}

// Elicit sends the elicitation request to the MCP server and returns the result.
func (p *ProxyElicitationHandler) Elicit(
	ctx context.Context,
	request mcp.ElicitationRequest,
) (*mcp.ElicitationResult, error) {
	if err := ensureMcpProxy(p.host); err != nil {
		return nil, err
	}

	sessionContext := p.host.proxyServer.WithContext(ctx, p.host.session)
	return p.host.proxyServer.RequestElicitation(sessionContext, request)
}

func ensureMcpProxy(host *McpHost) error {
	if host.proxyServer == nil {
		return fmt.Errorf("MCP host proxy server not set")
	}

	if host.session == nil {
		return fmt.Errorf("MCP host session not set")
	}

	return nil
}
