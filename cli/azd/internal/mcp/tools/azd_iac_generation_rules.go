// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"

	"github.com/azure/azure-dev/internal/mcp/tools/prompts"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewAzdIacGenerationRulesTool creates a new azd iac generation rules tool
func NewAzdIacGenerationRulesTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"iac_generation_rules",
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription(
				`Returns comprehensive rules and guidelines for generating Bicep Infrastructure as Code files and modules 
for AZD projects. 

The LLM agent should reference these rules when generating infrastructure code.

Use this tool when:
- Generating any Bicep infrastructure templates for AZD projects
- Need compliance rules and naming conventions for Azure resources
- Creating modular, reusable Bicep files
- Ensuring security and operational best practices"`,
			),
		),
		Handler: handleAzdIacGenerationRules,
	}
}

func handleAzdIacGenerationRules(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdIacRulesPrompt), nil
}
