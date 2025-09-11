// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/mcp/tools/prompts"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewAzdPlanInitTool creates a new azd plan init tool
func NewAzdPlanInitTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"azd_plan_init",
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription(
				`Provides instructions for analyzing workspace contents to determine project state, classify as "new project" or "existing application", route users to appropriate workflow, and execute the selected workflow with proper tool orchestration.

This tool returns detailed instructions that the LLM agent should follow using available analysis and orchestration tools.

Use this tool when:
- Starting AZD project initialization and need to determine the right workflow
- Workspace contains mixed or unclear content requiring analysis
- Need to route between new project creation vs application modernization
- Beginning any AZD project setup and need proper workflow guidance`,
			),
		),
		Handler: handleAzdPlanInit,
	}
}

func handleAzdPlanInit(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdPlanInitPrompt), nil
}
