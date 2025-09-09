// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/mcp/tools/prompts"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewAzdModernizeProjectTool creates a new azd project modernization tool
func NewAzdModernizeProjectTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"azd_modernize_project",
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription(
				`Returns instructions for modernizing existing applications to be AZD-compatible while preserving
existing functionality and structure.

The LLM agent should execute these instructions using available tools.

Use this tool when:
- Existing application code is detected in the workspace
- Need to add Azure deployment capabilities to existing projects
- Want to migrate existing applications to Azure with minimal disruption
- Application components and architecture need analysis and Azure service mapping`,
			),
		),
		Handler: handleAzdModernizeProject,
	}
}

func handleAzdModernizeProject(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdModernizeProjectPrompt), nil
}
