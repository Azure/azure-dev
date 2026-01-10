// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/mcp/tools/prompts"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewAzdErrorTroubleShootingTool creates a new azd error troubleshooting tool
func NewAzdErrorTroubleShootingTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"error_troubleshooting",
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription(
				`Returns instructions for diagnosing errors from Azure Developer CLI (azd) commands and provides 
step-by-step troubleshooting instructions.

The LLM agent should execute these instructions using available tools.

Use this tool when:
- Any error occurs during Azure Developer CLI (azd) command execution
- Need to classify, analyze, and resolve errors automatically or with guided steps
- Provide troubleshooting steps for errors`,
			),
		),
		Handler: handleAzdErrorTroubleShooting,
	}
}

func handleAzdErrorTroubleShooting(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdErrorTroubleShootingPrompt), nil
}
