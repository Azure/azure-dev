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
			mcp.WithDescription(
				`Returns instructions for diagnosing any error from azd commands and providing suggested actions for resolution.

The LLM agent should execute these instructions using available tools.

Use this tool when:
- Error occurs when running azd commands
- Need to identify the type of error and get actionable suggestions
- Ready to troubleshoot errors`,
			),
		),
		Handler: handleAzdErrorTroubleShooting,
	}
}

func handleAzdErrorTroubleShooting(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdErrorTroubleShootingPrompt), nil
}
