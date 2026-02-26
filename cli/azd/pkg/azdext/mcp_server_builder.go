// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"golang.org/x/time/rate"
)

// MCPToolHandler is the handler function signature for MCP tools.
// It receives parsed ToolArgs and the server's security policy (may be nil).
type MCPToolHandler func(ctx context.Context, args ToolArgs) (*mcp.CallToolResult, error)

// MCPToolOptions configures an MCP tool registration.
type MCPToolOptions struct {
	Description string
	Title       string // TitleAnnotation
	ReadOnly    bool   // ReadOnlyHint annotation
	Idempotent  bool   // IdempotentHint annotation
	Destructive bool   // DestructiveHint annotation
}

// serverToolEntry stores a pending tool registration.
type serverToolEntry struct {
	name    string
	handler MCPToolHandler
	opts    MCPToolOptions
	params  []mcp.ToolOption
}

// MCPServerBuilder provides a fluent API for building MCP servers with middleware.
type MCPServerBuilder struct {
	name           string
	version        string
	rateLimiter    *rate.Limiter
	securityPolicy *MCPSecurityPolicy
	tools          []serverToolEntry
	serverOpts     []server.ServerOption
	resources      []server.ServerResource
}

// NewMCPServerBuilder creates a new builder for an MCP server.
func NewMCPServerBuilder(name, version string) *MCPServerBuilder {
	return &MCPServerBuilder{
		name:    name,
		version: version,
		tools:   []serverToolEntry{},
	}
}

// WithRateLimit configures a token bucket rate limiter.
// burst is the maximum number of concurrent requests, refillRate is tokens per second.
func (b *MCPServerBuilder) WithRateLimit(burst int, refillRate float64) *MCPServerBuilder {
	b.rateLimiter = rate.NewLimiter(rate.Limit(refillRate), burst)
	return b
}

// WithSecurityPolicy attaches a security policy for URL/path validation.
// The policy is available to tool handlers via [MCPServerBuilder.SecurityPolicy].
// Tool handlers should call CheckURL/CheckPath on it for relevant parameters,
// since the builder cannot automatically determine which arguments are URLs or paths.
func (b *MCPServerBuilder) WithSecurityPolicy(policy *MCPSecurityPolicy) *MCPServerBuilder {
	b.securityPolicy = policy
	return b
}

// AddTool registers a tool with the server.
// The handler receives parsed ToolArgs (not raw mcp.CallToolRequest).
// Rate limiting is automatically applied. For URL/path security validation,
// use [MCPServerBuilder.SecurityPolicy] within the handler.
// params defines the tool's input parameters as mcp.ToolOption items.
func (b *MCPServerBuilder) AddTool(
	name string,
	handler MCPToolHandler,
	opts MCPToolOptions,
	params ...mcp.ToolOption,
) *MCPServerBuilder {
	b.tools = append(b.tools, serverToolEntry{
		name:    name,
		handler: handler,
		opts:    opts,
		params:  params,
	})
	return b
}

// WithInstructions sets system instructions that guide AI clients on how to use the server's tools.
func (b *MCPServerBuilder) WithInstructions(instructions string) *MCPServerBuilder {
	b.serverOpts = append(b.serverOpts, server.WithInstructions(instructions))
	return b
}

// WithResourceCapabilities enables resource support on the server.
// subscribe controls whether clients can subscribe to resource changes.
// listChanged controls whether the server notifies clients when the resource list changes.
func (b *MCPServerBuilder) WithResourceCapabilities(subscribe, listChanged bool) *MCPServerBuilder {
	b.serverOpts = append(b.serverOpts, server.WithResourceCapabilities(subscribe, listChanged))
	return b
}

// WithPromptCapabilities enables prompt support on the server.
// listChanged controls whether the server notifies clients when the prompt list changes.
func (b *MCPServerBuilder) WithPromptCapabilities(listChanged bool) *MCPServerBuilder {
	b.serverOpts = append(b.serverOpts, server.WithPromptCapabilities(listChanged))
	return b
}

// WithServerOption adds a raw mcp-go server option for capabilities not directly exposed by the builder.
func (b *MCPServerBuilder) WithServerOption(opt server.ServerOption) *MCPServerBuilder {
	b.serverOpts = append(b.serverOpts, opt)
	return b
}

// AddResources registers static resources with the server.
func (b *MCPServerBuilder) AddResources(resources ...server.ServerResource) *MCPServerBuilder {
	b.resources = append(b.resources, resources...)
	return b
}

// Build creates the configured MCP server ready to serve.
func (b *MCPServerBuilder) Build() *server.MCPServer {
	// Always enable tool capabilities, plus any user-configured server options
	opts := append([]server.ServerOption{server.WithToolCapabilities(true)}, b.serverOpts...)
	mcpServer := server.NewMCPServer(b.name, b.version, opts...)

	serverTools := make([]server.ServerTool, 0, len(b.tools))
	for _, entry := range b.tools {
		// Build tool options: description + annotations + user-provided params
		toolOpts := make([]mcp.ToolOption, 0, len(entry.params)+5)
		if entry.opts.Description != "" {
			toolOpts = append(toolOpts, mcp.WithDescription(entry.opts.Description))
		}
		if entry.opts.Title != "" {
			toolOpts = append(toolOpts, mcp.WithTitleAnnotation(entry.opts.Title))
		}
		toolOpts = append(toolOpts, mcp.WithReadOnlyHintAnnotation(entry.opts.ReadOnly))
		toolOpts = append(toolOpts, mcp.WithIdempotentHintAnnotation(entry.opts.Idempotent))
		toolOpts = append(toolOpts, mcp.WithDestructiveHintAnnotation(entry.opts.Destructive))
		toolOpts = append(toolOpts, entry.params...)

		tool := mcp.NewTool(entry.name, toolOpts...)

		// Wrap the user handler with middleware
		wrappedHandler := b.wrapHandler(entry.name, entry.handler)

		serverTools = append(serverTools, server.ServerTool{
			Tool:    tool,
			Handler: wrappedHandler,
		})
	}

	mcpServer.AddTools(serverTools...)

	if len(b.resources) > 0 {
		mcpServer.AddResources(b.resources...)
	}

	return mcpServer
}

// SecurityPolicy returns the configured security policy, or nil if none was set.
// Tool handlers should use this to validate URLs and file paths:
//
//	if policy := builder.SecurityPolicy(); policy != nil {
//	    if err := policy.CheckURL(url); err != nil {
//	        return MCPErrorResult("blocked: %v", err), nil
//	    }
//	}
func (b *MCPServerBuilder) SecurityPolicy() *MCPSecurityPolicy {
	return b.securityPolicy
}

// wrapHandler creates the mcp-go compatible handler that applies rate limiting
// and argument parsing before delegating to the user's MCPToolHandler.
func (b *MCPServerBuilder) wrapHandler(
	toolName string,
	handler MCPToolHandler,
) func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limiter := b.rateLimiter

	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Rate limiting check
		if limiter != nil && !limiter.Allow() {
			slog.Warn("rate limit exceeded for MCP tool", "tool", toolName)
			return MCPErrorResult("rate limit exceeded, please retry"), nil
		}

		// Parse arguments into typed ToolArgs
		args := ParseToolArgs(request)

		return handler(ctx, args)
	}
}
