// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/mcp/tools/prompts"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewAzdSelectStackTool creates a new azd stack selection tool
func NewAzdSelectStackTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"azd_select_stack",
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription(
				`Provides instructions for assessing team expertise, application characteristics, and project requirements through targeted questions to select the optimal single technology stack (Containers, Serverless, or Logic Apps) with clear rationale.

This tool returns detailed instructions that the LLM agent should follow using available user interaction and documentation tools.

Use this tool when:
- User intent and project requirements have been discovered
- Ready to determine the optimal technology stack for the project
- Need to assess team capabilities and application characteristics
- Project requirements are understood and ready for technology decisions`,
			),
		),
		Handler: handleAzdSelectStack,
	}
}

func handleAzdSelectStack(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdSelectStackPrompt), nil
}
