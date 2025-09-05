// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/mcp/tools/prompts"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewAzdCommonErrorTool creates a new azd common error tool
func NewAzdCommonErrorTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"azd_common_error",
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription(
				`Returns instructions for diagnosing common error type and providing suggested actions for resolution.

The LLM agent should execute these instructions using available tools.

Use this tool when:
- Need to identify the type of error and get actionable suggestions
- Ready to troubleshoot errors`,
			),
		),
		Handler: handleAzdCommonError,
	}
}

func handleAzdCommonError(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdCommonErrorPrompt), nil
}
