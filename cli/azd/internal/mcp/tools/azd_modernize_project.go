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
				`Provides instructions for modernizing existing applications to be AZD-compatible by conducting analysis, requirements discovery, stack selection, architecture planning, and artifact generation while preserving existing functionality.

This tool returns detailed instructions that the LLM agent should follow using available analysis, planning, and generation tools.

Use this tool when:
- Working with an existing application that needs Azure deployment capabilities
- Ready to modernize a project to be AZD-compatible while preserving functionality
- Need to conduct comprehensive analysis and generate Azure deployment artifacts
- Application workspace contains existing code that should be preserved`,
			),
		),
		Handler: handleAzdModernizeProject,
	}
}

func handleAzdModernizeProject(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdModernizeProjectPrompt), nil
}
