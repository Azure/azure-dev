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
				`Returns instructions for orchestrating complete AZD project artifact generation including
application scaffolding, Docker configurations, infrastructure templates, and azure.yaml configuration.

The LLM agent should execute these instructions using available tools.

Use this tool when:
- Project architecture and requirements have been defined in Application specification
- Ready to generate all project artifacts and implementation files
- Need to create application code, infrastructure templates, and deployment configurations
- Moving from planning phase to implementation phase`,
			),
		),
		Handler: handleAzdArtifactGeneration,
	}
}

func handleAzdArtifactGeneration(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdArtifactGenerationPrompt), nil
}
