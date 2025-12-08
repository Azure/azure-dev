// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/mcp/tools/prompts"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewAzdInfrastructureGenerationTool creates a new azd infrastructure generation tool
func NewAzdInfrastructureGenerationTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"infrastructure_generation",
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription(
				`Returns instructions for generating modular Bicep infrastructure templates following Azure security and 
operational best practices for AZD projects. 

The LLM agent should execute these instructions using available tools.

Use this tool when:
- Architecture planning completed with Azure services selected
- Need to create Bicep infrastructure templates
- Ready to implement infrastructure as code for deployment`,
			),
		),
		Handler: handleAzdInfrastructureGeneration,
	}
}

func handleAzdInfrastructureGeneration(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdInfrastructureGenerationPrompt), nil
}
