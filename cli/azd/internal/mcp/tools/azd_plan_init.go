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
				`Initial entry point for AZD project initialization and modernization workflows.

This tool provides orchestration guidance for transforming workspaces into AZD-compatible projects.

Use this tool when:
- Creating new AZD project from an empty workspace
- Modernizing an existing application to become AZD compatible
- Need guidance for complete project setup and configuration

Will help with the following:
- Analysis & Discovery
- Architecture Planning including stack selection
- File / Code generation
- Project validation`,
			),
		),
		Handler: handleAzdPlanInit,
	}
}

func handleAzdPlanInit(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdPlanInitPrompt), nil
}
