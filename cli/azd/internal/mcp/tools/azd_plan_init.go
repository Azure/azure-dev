// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"

	"github.com/azure/azure-dev/internal/mcp/tools/prompts"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewAzdPlanInitTool creates a new azd plan init tool
func NewAzdPlanInitTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"plan_init",
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription(
				`Returns instructions for orchestrating complete AZD application initialization using structured phases 
with specialized tools. 

The LLM agent should execute these instructions using available tools.

Use this tool when:
- Starting new AZD project initialization or migration
- Need structured approach to transform application into AZD-compatible project
- Want to ensure proper sequencing of discovery, planning, and file generation
- Require complete project orchestration guidance`,
			),
		),
		Handler: handleAzdPlanInit,
	}
}

func handleAzdPlanInit(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdPlanInitPrompt), nil
}
