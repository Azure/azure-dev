// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package common

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
)

// ErrorResponse represents a JSON error response structure that can be reused across all tools
type ErrorResponse struct {
	Error   bool   `json:"error"`
	Message string `json:"message"`
}

type Tool interface {
	Name() string
	Server() string
	Description() string
	Call(ctx context.Context, input string) (string, error)
}

// AnnotatedTool extends the Tool interface with MCP annotations
type AnnotatedTool interface {
	Tool
	// Annotations returns MCP tool behavior annotations
	Annotations() mcp.ToolAnnotation
}

type BuiltInTool struct {
}

func (t *BuiltInTool) Server() string {
	return "built-in"
}
