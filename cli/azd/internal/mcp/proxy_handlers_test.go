// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/v2/mcp"
	"github.com/mark3labs/mcp-go/v2/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type samplingHandlerFunc func(context.Context, mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error)

func (f samplingHandlerFunc) CreateMessage(
	ctx context.Context, request mcp.CreateMessageRequest,
) (*mcp.CreateMessageResult, error) {
	return f(ctx, request)
}

func TestProxySamplingHandlerLegacyInitializedSession(t *testing.T) {
	request := mcp.CreateMessageRequest{CreateMessageParams: mcp.CreateMessageParams{MaxTokens: 500}}
	expected := &mcp.CreateMessageResult{Model: "test-model"}
	session := server.NewInProcessSession("test", samplingHandlerFunc(
		func(_ context.Context, actual mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
			require.Equal(t, request, actual)
			return expected, nil
		}))
	proxyServer := server.NewMCPServer("test", "1.0.0")
	ctx := proxyServer.WithContext(t.Context(), session)
	initialize := json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{
		"protocolVersion":"2026-07-28","capabilities":{"sampling":{}},
		"clientInfo":{"name":"test","version":"1.0.0"}}}`)
	response := proxyServer.HandleMessage(ctx, initialize)
	encoded, err := json.Marshal(response)
	require.NoError(t, err)
	var initialized struct {
		Error *json.RawMessage `json:"error"`
	}
	require.NoError(t, json.Unmarshal(encoded, &initialized))
	require.Nil(t, initialized.Error)
	require.Equal(t, mcp.LATEST_LEGACY_PROTOCOL_VERSION, session.ProtocolVersion())
	host := NewMcpHost()
	host.SetProxyServer(proxyServer)
	host.SetSession(session)
	result, err := (&ProxySamplingHandler{host: host}).CreateMessage(ctx, request)
	require.NoError(t, err)
	require.Same(t, expected, result)
}

func TestNewProxySamplingHandler(t *testing.T) {
	handler := NewProxySamplingHandler()
	require.NotNil(t, handler)

	concrete, ok := handler.(*ProxySamplingHandler)
	require.True(t, ok, "expected *ProxySamplingHandler")
	assert.Nil(t, concrete.host, "host should be nil initially")
}

func TestNewProxyElicitationHandler(t *testing.T) {
	handler := NewProxyElicitationHandler()
	require.NotNil(t, handler)

	concrete, ok := handler.(*ProxyElicitationHandler)
	require.True(t, ok, "expected *ProxyElicitationHandler")
	assert.Nil(t, concrete.host, "host should be nil initially")
}

func TestEnsureMcpProxy(t *testing.T) {
	nonNilServer := &server.MCPServer{}

	tests := []struct {
		name        string
		setupHost   func() *McpHost
		expectErr   bool
		errContains string
	}{
		{
			name: "nil proxy server",
			setupHost: func() *McpHost {
				h := NewMcpHost()
				h.session = &simpleSession{}
				return h
			},
			expectErr:   true,
			errContains: "MCP host proxy server not set",
		},
		{
			name: "nil session",
			setupHost: func() *McpHost {
				h := NewMcpHost()
				h.proxyServer = nonNilServer
				return h
			},
			expectErr:   true,
			errContains: "MCP host session not set",
		},
		{
			name: "both nil",
			setupHost: func() *McpHost {
				return NewMcpHost()
			},
			expectErr:   true,
			errContains: "MCP host proxy server not set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := tt.setupHost()
			err := ensureMcpProxy(host)

			if tt.expectErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestProxySamplingHandler_CreateMessage_NilHost(t *testing.T) {
	// ensureMcpProxy dereferences host without a nil check,
	// so a nil host is a programming error that panics.
	handler := &ProxySamplingHandler{}
	ctx := t.Context()
	req := mcp.CreateMessageRequest{}

	assert.Panics(t, func() {
		_, _ = handler.CreateMessage(ctx, req)
	})
}

func TestProxySamplingHandler_CreateMessage_NoProxyServer(t *testing.T) {
	host := NewMcpHost()
	handler := &ProxySamplingHandler{host: host}

	ctx := t.Context()
	req := mcp.CreateMessageRequest{}

	result, err := handler.CreateMessage(ctx, req)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "MCP host proxy server not set")
}

func TestProxySamplingHandler_CreateMessage_NoSession(t *testing.T) {
	host := NewMcpHost()
	host.proxyServer = &server.MCPServer{}
	handler := &ProxySamplingHandler{host: host}

	ctx := t.Context()
	req := mcp.CreateMessageRequest{}

	result, err := handler.CreateMessage(ctx, req)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "MCP host session not set")
}

func TestProxyElicitationHandler_Elicit_NilHost(t *testing.T) {
	// ensureMcpProxy dereferences host without a nil check,
	// so a nil host is a programming error that panics.
	handler := &ProxyElicitationHandler{}
	ctx := t.Context()
	req := mcp.ElicitationRequest{}

	assert.Panics(t, func() {
		_, _ = handler.Elicit(ctx, req)
	})
}

func TestProxyElicitationHandler_Elicit_NoProxyServer(t *testing.T) {
	host := NewMcpHost()
	handler := &ProxyElicitationHandler{host: host}

	ctx := t.Context()
	req := mcp.ElicitationRequest{}

	result, err := handler.Elicit(ctx, req)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "MCP host proxy server not set")
}

func TestProxyElicitationHandler_Elicit_NoSession(t *testing.T) {
	host := NewMcpHost()
	host.proxyServer = &server.MCPServer{}
	handler := &ProxyElicitationHandler{host: host}

	ctx := t.Context()
	req := mcp.ElicitationRequest{}

	result, err := handler.Elicit(ctx, req)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "MCP host session not set")
}

func TestEnsureMcpProxy_BothSet(t *testing.T) {
	host := NewMcpHost()
	host.proxyServer = &server.MCPServer{}
	host.session = &simpleSession{}

	err := ensureMcpProxy(host)

	require.NoError(t, err)
}
