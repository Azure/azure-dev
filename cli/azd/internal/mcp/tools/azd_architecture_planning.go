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
			"architecture_planning",
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription(
				`Returns instructions for selecting appropriate Azure services for discovered application components and 
designing infrastructure architecture. 

The LLM agent should execute these instructions using available tools.

Use this tool when:
- Discovery analysis has been completed and azd-arch-plan.md exists
- Application components have been identified and classified
- Need to map components to Azure hosting services
- Ready to plan containerization and database strategies`,
			),
		),
		Handler: handleAzdArchitecturePlanning,
	}
}

func handleAzdArchitecturePlanning(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdArchitecturePlanningPrompt), nil
}
