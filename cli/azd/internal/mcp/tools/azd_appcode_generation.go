// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/mcp/tools/prompts"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewAzdAppCodeGenerationTool creates a new azd application code generation tool
func NewAzdAppCodeGenerationTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"azd_appcode_generation",
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription(
				`Returns instructions for generating application code scaffolding for all project components
using preferred programming languages and frameworks in src/<component> structure.

The LLM agent should execute these instructions using available tools.

Use this tool when:
- Project components have been defined and technology stack selected
- Programming language and framework preferences are known
- Need to generate application scaffolding for APIs, SPAs, workers, functions, etc.
- Ready to create production-ready code templates with Azure integrations`,
			),
		),
		Handler: handleAzdAppCodeGeneration,
	}
}

func handleAzdAppCodeGeneration(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdAppCodeGenerationPrompt), nil
}
