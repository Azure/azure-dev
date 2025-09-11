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
			"azd_infrastructure_generation",
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription(
				`Provides instructions for generating complete Bicep infrastructure templates in ./infra directory based on the Infrastructure as Code File Checklist, using latest schema versions and following IaC generation rules.

This tool returns detailed instructions that the LLM agent should follow using available infrastructure generation tools.

Use this tool when:
- Application architecture planning is complete with Azure service mappings
- Ready to generate Bicep templates for all required Azure resources
- Infrastructure as Code File Checklist exists in the application spec
- Need to create modular, reusable infrastructure templates following best practices`,
			),
		),
		Handler: handleAzdInfrastructureGeneration,
	}
}

func handleAzdInfrastructureGeneration(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdInfrastructureGenerationPrompt), nil
}
