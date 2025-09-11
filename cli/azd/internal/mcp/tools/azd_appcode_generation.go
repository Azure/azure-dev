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
				`Provides instructions for generating production-ready application scaffolding and starter code for all application components with Azure SDK integrations and deployment-ready configurations.

This tool returns detailed instructions that the LLM agent should follow using available code generation tools.

Use this tool when:
- Application components and technology stack have been defined in the application spec
- Ready to create code structure in src/<component> directories
- Need to generate framework-specific project files with Azure integrations
- Application architecture planning is complete and ready for implementation`,
			),
		),
		Handler: handleAzdAppCodeGeneration,
	}
}

func handleAzdAppCodeGeneration(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdAppCodeGenerationPrompt), nil
}
