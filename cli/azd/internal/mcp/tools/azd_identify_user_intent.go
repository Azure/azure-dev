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
				`Provides instructions for engaging users with conversational questions to understand project purpose, scale requirements, budget constraints, and architectural preferences, then classify and document findings.

This tool returns detailed instructions that the LLM agent should follow using available user interaction and documentation tools.

Use this tool when:
- Starting a new project and need to understand user requirements
- Beginning the discovery phase for project planning
- Need to classify project type, scale, and budget constraints
- Ready to gather architectural and technology preferences from the user`,
			),
		),
		Handler: handleAzdIdentifyUserIntent,
	}
}

func handleAzdIdentifyUserIntent(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdIdentifyUserIntentPrompt), nil
}
