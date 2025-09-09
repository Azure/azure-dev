// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/mcp/tools/prompts"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewAzdNewProjectTool creates a new azd new project creation orchestration tool
func NewAzdNewProjectTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"azd_new_project",
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription(
				`Returns instructions for orchestrating complete new AZD project creation from scratch,
including requirements discovery, stack selection, architecture planning, and implementation roadmap.

The LLM agent should execute these instructions using available tools.

Use this tool when:
- Creating a completely new AZD project with no existing code
- User has a clear idea of what they want to build
- Workspace is empty or contains only minimal documentation
- Need comprehensive project specification and architecture design`,
			),
		),
		Handler: handleAzdNewProject,
	}
}

func handleAzdNewProject(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdNewProjectPrompt), nil
}
