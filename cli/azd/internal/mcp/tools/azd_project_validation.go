// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"

	"github.com/azure/azure-dev/internal/mcp/tools/prompts"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewAzdProjectValidationTool creates a new azd project validation tool
func NewAzdProjectValidationTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"project_validation",
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription(
				`Returns instructions for validating AZD project by running comprehensive checks on azure.yaml schema, 
Bicep templates, environment setup, packaging, and deployment preview.

The LLM agent should execute these instructions using available tools.

Use this tool when:
- All AZD configuration files have been generated
- Ready to validate complete project before deployment
- Need to ensure azure.yaml, Bicep templates, and environment are properly configured
- Final validation step before running azd up`,
			),
		),
		Handler: handleAzdProjectValidation,
	}
}

func handleAzdProjectValidation(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdProjectValidationPrompt), nil
}
