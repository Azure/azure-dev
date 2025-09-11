// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/mcp/tools/prompts"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewAzdArtifactGenerationTool creates a new azd artifact generation orchestration tool
func NewAzdArtifactGenerationTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"azd_artifact_generation",
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription(
				`Provides instructions for orchestrating the complete artifact generation process for AZD projects, generating infrastructure templates, application scaffolding, Docker configurations, and azure.yaml in the correct order with proper dependencies.

This tool returns detailed instructions that the LLM agent should follow using available generation tools.

Use this tool when:
- Application architecture design is complete with all service mappings
- Ready to generate all project artifacts (infrastructure, code, Docker, azure.yaml)
- Need to coordinate multiple generation processes in proper dependency order
- Project specification contains complete requirements for artifact generation`,
			),
		),
		Handler: handleAzdArtifactGeneration,
	}
}

func handleAzdArtifactGeneration(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdArtifactGenerationPrompt), nil
}
