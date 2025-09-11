// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/mcp/tools/prompts"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewAzdProjectValidationTool creates a new azd project validation tool
func NewAzdProjectValidationTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"azd_project_validation",
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription(
				`Provides instructions for executing comprehensive validation of AZD project components including azure.yaml schema, Bicep templates, environment configuration, packaging, and deployment preview to ensure deployment readiness.

This tool returns detailed instructions that the LLM agent should follow using available validation and testing tools.

Use this tool when:
- All AZD configuration files and artifacts have been generated
- Ready to validate complete project before deployment with azd up
- Need to ensure azure.yaml, Bicep templates, and environment are properly configured
- Final validation step to confirm deployment readiness`,
			),
		),
		Handler: handleAzdProjectValidation,
	}
}

func handleAzdProjectValidation(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdProjectValidationPrompt), nil
}
