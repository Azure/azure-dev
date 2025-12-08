// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package common

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tmc/langchaingo/tools"
)

// ToolLoader provides an interface for loading tools from different categories
type ToolLoader interface {
	LoadTools(ctx context.Context) ([]AnnotatedTool, error)
}

// ErrorResponse represents a JSON error response structure that can be reused across all tools
type ErrorResponse struct {
	Error   bool   `json:"error"`
	Message string `json:"message"`
}

// Tool extends the tools.Tool interface with a Server method to identify the tool's server
type Tool interface {
	tools.Tool
	Server() string
}

// AnnotatedTool extends the Tool interface with MCP annotations
type AnnotatedTool interface {
	Tool
	// Annotations returns MCP tool behavior annotations
	Annotations() mcp.ToolAnnotation
}

// BuiltInTool represents a built-in tool
type BuiltInTool struct {
}

// Server returns the server name for the built-in tool
func (t *BuiltInTool) Server() string {
	return "built-in"
}
