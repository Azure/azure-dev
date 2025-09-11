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
				`Provides instructions for orchestrating complete new project creation from scratch through requirements discovery, stack selection, architecture planning, and file generation guidance to create a ready-to-implement project specification.

This tool returns detailed instructions that the LLM agent should follow using available discovery, planning, and generation tools.

Use this tool when:
- Creating a brand new AZD project from an empty or minimal workspace
- Ready to guide user through complete project setup from requirements to implementation
- Need systematic workflow for new project creation with all planning phases
- Starting fresh without existing application code or infrastructure`,
			),
		),
		Handler: handleAzdNewProject,
	}
}

func handleAzdNewProject(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdNewProjectPrompt), nil
}
