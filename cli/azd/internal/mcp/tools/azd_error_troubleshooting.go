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
			"azd_error_troubleshooting",
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription(
				`Returns instructions to identify, explain and diagnose the errors from Azure Developer CLI (azd) commands and provides step-by-step troubleshooting instructions.

The LLM agent should execute these instructions using available tools.

Use this tool when:
- Request to identify, explain and diagnose the error when running azd command and its root cause
- Provide actionable troubleshooting steps`,
			),
		),
		Handler: handleAzdErrorTroubleShooting,
	}
}

func handleAzdErrorTroubleShooting(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdErrorTroubleShootingPrompt), nil
}
