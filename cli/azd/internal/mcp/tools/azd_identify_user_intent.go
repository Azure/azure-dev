// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/mcp/tools/prompts"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewAzdIdentifyUserIntentTool creates a new azd identify user intent tool
func NewAzdIdentifyUserIntentTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"azd_identify_user_intent",
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription(
				`Returns instructions for discovering user project intent, requirements, and technology preferences
through conversational questioning.

The LLM agent should execute these instructions using available tools.

Use this tool when:
- Starting requirements discovery for new or existing projects
- Need to understand project scale, budget constraints, and architectural preferences
- Want to capture programming language and framework preferences
- Beginning project planning phase to inform technology stack decisions`,
			),
		),
		Handler: handleAzdIdentifyUserIntent,
	}
}

func handleAzdIdentifyUserIntent(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdIdentifyUserIntentPrompt), nil
}
