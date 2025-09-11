// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/mcp/tools/prompts"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewAzdDockerGenerationTool creates a new azd docker generation tool
func NewAzdDockerGenerationTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"azd_docker_generation",
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription(
				`Provides instructions for generating optimized Dockerfiles and .dockerignore files for all containerizable services based on the Docker File Generation Checklist from the application spec.

This tool returns detailed instructions that the LLM agent should follow using available file creation tools.

Use this tool when:
- Application components requiring containerization have been identified
- Docker File Generation Checklist exists in the application spec
- Ready to create production-ready Dockerfiles with multi-stage builds and security best practices
- Need to generate .dockerignore files for build optimization`,
			),
		),
		Handler: handleAzdDockerGeneration,
	}
}

func handleAzdDockerGeneration(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdDockerGenerationPrompt), nil
}
