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
				`Returns instructions for selecting the optimal technology stack (Containers, Serverless, or Logic Apps)
based on team expertise, performance requirements, and application characteristics.

The LLM agent should execute these instructions using available tools.

Use this tool when:
- User intent discovery has been completed
- Need to determine the best technology approach for the project
- Team expertise and application requirements are understood
- Ready to make technology stack decisions for architecture planning`,
			),
		),
		Handler: handleAzdSelectStack,
	}
}

func handleAzdSelectStack(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdSelectStackPrompt), nil
}
