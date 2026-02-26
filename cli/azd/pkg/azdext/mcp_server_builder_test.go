// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPServerBuilder_Build_ReturnsValidServer(t *testing.T) {
	builder := NewMCPServerBuilder("test-server", "1.0.0")
	srv := builder.Build()
	require.NotNil(t, srv)
}

func TestMCPServerBuilder_AddTool_RegistersTools(t *testing.T) {
	handler := func(ctx context.Context, args ToolArgs) (*mcp.CallToolResult, error) {
		return MCPTextResult("ok"), nil
	}

	srv := NewMCPServerBuilder("test-server", "1.0.0").
		AddTool("tool_a", handler, MCPToolOptions{Description: "Tool A"}).
		AddTool("tool_b", handler, MCPToolOptions{Description: "Tool B"}).
		Build()

	require.NotNil(t, srv)
}

func TestMCPServerBuilder_FluentChaining(t *testing.T) {
	handler := func(ctx context.Context, args ToolArgs) (*mcp.CallToolResult, error) {
		return MCPTextResult("ok"), nil
	}

	policy := NewMCPSecurityPolicy().RequireHTTPS()

	builder := NewMCPServerBuilder("test-server", "1.0.0").
		WithRateLimit(10, 5.0).
		WithSecurityPolicy(policy).
		AddTool("tool_a", handler, MCPToolOptions{Description: "Tool A"})

	require.NotNil(t, builder)
	require.NotNil(t, builder.rateLimiter)
	require.NotNil(t, builder.securityPolicy)
	require.Len(t, builder.tools, 1)

	srv := builder.Build()
	require.NotNil(t, srv)
}

func TestMCPServerBuilder_HandlerReceivesParsedToolArgs(t *testing.T) {
	var receivedArgs ToolArgs

	handler := func(ctx context.Context, args ToolArgs) (*mcp.CallToolResult, error) {
		receivedArgs = args
		return MCPTextResult("ok"), nil
	}

	builder := NewMCPServerBuilder("test-server", "1.0.0").
		AddTool("echo", handler, MCPToolOptions{Description: "Echo tool"},
			mcp.WithString("message", mcp.Description("The message"), mcp.Required()),
		)

	// Build and get the wrapped handler by invoking through the server tool
	require.Len(t, builder.tools, 1)

	// Test the wrapper directly
	wrappedHandler := builder.wrapHandler("echo", handler)
	request := mcp.CallToolRequest{}
	request.Params.Name = "echo"
	request.Params.Arguments = map[string]interface{}{
		"message": "hello world",
	}

	result, err := wrappedHandler(context.Background(), request)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify the handler received parsed ToolArgs
	msg, argErr := receivedArgs.RequireString("message")
	require.NoError(t, argErr)
	assert.Equal(t, "hello world", msg)
}

func TestMCPServerBuilder_RateLimiting(t *testing.T) {
	callCount := 0
	handler := func(ctx context.Context, args ToolArgs) (*mcp.CallToolResult, error) {
		callCount++
		return MCPTextResult("ok"), nil
	}

	// burst=1 means only 1 token available immediately, refillRate very low
	builder := NewMCPServerBuilder("test-server", "1.0.0").
		WithRateLimit(1, 0.001)

	wrappedHandler := builder.wrapHandler("test", handler)
	request := mcp.CallToolRequest{}
	request.Params.Name = "test"
	request.Params.Arguments = map[string]interface{}{}

	// First call should succeed (consumes the 1 burst token)
	result1, err1 := wrappedHandler(context.Background(), request)
	require.NoError(t, err1)
	require.NotNil(t, result1)
	assert.Equal(t, 1, callCount)

	// Second call immediately should be rate limited
	result2, err2 := wrappedHandler(context.Background(), request)
	require.NoError(t, err2)
	require.NotNil(t, result2)
	assert.Equal(t, 1, callCount, "handler should not have been called again due to rate limiting")

	// Verify the rate limit error message
	require.True(t, result2.IsError, "result should be an error")
	require.Len(t, result2.Content, 1)
	textContent, ok := result2.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, textContent.Text, "rate limit exceeded")
}

func TestMCPServerBuilder_NoRateLimit(t *testing.T) {
	callCount := 0
	handler := func(ctx context.Context, args ToolArgs) (*mcp.CallToolResult, error) {
		callCount++
		return MCPTextResult("ok"), nil
	}

	// No rate limit configured
	builder := NewMCPServerBuilder("test-server", "1.0.0")

	wrappedHandler := builder.wrapHandler("test", handler)
	request := mcp.CallToolRequest{}
	request.Params.Name = "test"
	request.Params.Arguments = map[string]interface{}{}

	// Multiple rapid calls should all succeed
	for i := 0; i < 5; i++ {
		result, err := wrappedHandler(context.Background(), request)
		require.NoError(t, err)
		require.NotNil(t, result)
	}
	assert.Equal(t, 5, callCount)
}

func TestMCPServerBuilder_WithSecurityPolicy_StoresPolicy(t *testing.T) {
	policy := DefaultMCPSecurityPolicy()

	builder := NewMCPServerBuilder("test-server", "1.0.0").
		WithSecurityPolicy(policy)

	require.NotNil(t, builder.securityPolicy)
	assert.Equal(t, policy, builder.securityPolicy)
}

func TestMCPServerBuilder_SecurityPolicy_Accessor(t *testing.T) {
	// Without policy — returns nil.
	builder := NewMCPServerBuilder("test", "1.0.0")
	assert.Nil(t, builder.SecurityPolicy())

	// With policy — returns the stored policy.
	policy := DefaultMCPSecurityPolicy()
	builder.WithSecurityPolicy(policy)
	assert.Equal(t, policy, builder.SecurityPolicy())
}

func TestMCPServerBuilder_ToolOptions_Annotations(t *testing.T) {
	handler := func(ctx context.Context, args ToolArgs) (*mcp.CallToolResult, error) {
		return MCPTextResult("ok"), nil
	}

	builder := NewMCPServerBuilder("test-server", "1.0.0").
		AddTool("readonly_tool", handler, MCPToolOptions{
			Description: "A read-only tool",
			ReadOnly:    true,
			Destructive: false,
		}).
		AddTool("destructive_tool", handler, MCPToolOptions{
			Description: "A destructive tool",
			ReadOnly:    false,
			Destructive: true,
		})

	require.Len(t, builder.tools, 2)
	assert.True(t, builder.tools[0].opts.ReadOnly)
	assert.False(t, builder.tools[0].opts.Destructive)
	assert.False(t, builder.tools[1].opts.ReadOnly)
	assert.True(t, builder.tools[1].opts.Destructive)

	// Build should succeed
	srv := builder.Build()
	require.NotNil(t, srv)
}

func TestMCPServerBuilder_WithInstructions(t *testing.T) {
	builder := NewMCPServerBuilder("test-server", "1.0.0").
		WithInstructions("Use these tools to manage services.")

	require.Len(t, builder.serverOpts, 1)
	srv := builder.Build()
	require.NotNil(t, srv)
}

func TestMCPServerBuilder_WithResourceCapabilities(t *testing.T) {
	handler := func(ctx context.Context, args ToolArgs) (*mcp.CallToolResult, error) {
		return MCPTextResult("ok"), nil
	}

	builder := NewMCPServerBuilder("test-server", "1.0.0").
		WithResourceCapabilities(false, true).
		AddTool("test", handler, MCPToolOptions{Description: "Test"})

	require.Len(t, builder.serverOpts, 1)
	srv := builder.Build()
	require.NotNil(t, srv)
}

func TestMCPServerBuilder_WithPromptCapabilities(t *testing.T) {
	builder := NewMCPServerBuilder("test-server", "1.0.0").
		WithPromptCapabilities(false)

	require.Len(t, builder.serverOpts, 1)
	srv := builder.Build()
	require.NotNil(t, srv)
}

func TestMCPServerBuilder_WithServerOption(t *testing.T) {
	builder := NewMCPServerBuilder("test-server", "1.0.0").
		WithServerOption(server.WithLogging())

	require.Len(t, builder.serverOpts, 1)
	srv := builder.Build()
	require.NotNil(t, srv)
}

func TestMCPServerBuilder_AddResources(t *testing.T) {
	resource := server.ServerResource{
		Resource: mcp.Resource{
			URI:  "test://resource",
			Name: "test-resource",
		},
		Handler: func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			return []mcp.ResourceContents{mcp.TextResourceContents{Text: "hello"}}, nil
		},
	}

	builder := NewMCPServerBuilder("test-server", "1.0.0").
		WithResourceCapabilities(false, true).
		AddResources(resource)

	require.Len(t, builder.resources, 1)
	srv := builder.Build()
	require.NotNil(t, srv)
}

func TestMCPServerBuilder_FullServerSetup(t *testing.T) {
	handler := func(ctx context.Context, args ToolArgs) (*mcp.CallToolResult, error) {
		return MCPTextResult("ok"), nil
	}

	resource := server.ServerResource{
		Resource: mcp.Resource{
			URI:  "azure://project/azure.yaml",
			Name: "azure.yaml",
		},
		Handler: func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			return []mcp.ResourceContents{mcp.TextResourceContents{Text: "name: test"}}, nil
		},
	}

	policy := DefaultMCPSecurityPolicy()

	srv := NewMCPServerBuilder("app-mcp-server", "1.0.0").
		WithRateLimit(10, 1.0).
		WithSecurityPolicy(policy).
		WithInstructions("This MCP server provides runtime operations.").
		WithResourceCapabilities(false, true).
		WithPromptCapabilities(false).
		AddTool("get_services", handler, MCPToolOptions{
			Description: "Get running services",
			Title:       "Get Running Services",
			ReadOnly:    true,
			Idempotent:  true,
		}).
		AddResources(resource).
		Build()

	require.NotNil(t, srv)
}
