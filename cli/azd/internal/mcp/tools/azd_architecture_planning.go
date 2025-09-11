// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/mcp/tools/prompts"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewAzdArchitecturePlanningTool creates a new azd architecture planning tool
func NewAzdArchitecturePlanningTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"azd_architecture_planning",
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription(
				`Provides instructions for consolidating all previously gathered context (requirements, stack selection, discovered components) into a complete application architecture design with Azure service mappings and implementation strategy.

This tool returns detailed instructions that the LLM agent should follow using available planning and documentation tools.

Use this tool when:
- Discovery analysis has been completed and application components are identified
- Technology stack selection is complete
- Ready to map components to Azure hosting services and design infrastructure
- Need to create comprehensive architecture documentation in the application spec`,
			),
		),
		Handler: handleAzdArchitecturePlanning,
	}
}

func handleAzdArchitecturePlanning(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdArchitecturePlanningPrompt), nil
}
