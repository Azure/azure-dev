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
			"docker_generation",
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription(
				`Returns instructions for generating optimized Dockerfiles and container configurations for containerizable 
services in AZD projects. 

The LLM agent should execute these instructions using available tools.

Use this tool when:
- Architecture planning identified services requiring containerization
- azd-arch-plan.md shows Container Apps or AKS as selected hosting platform
- Need Dockerfiles for microservices, APIs, or containerized web applications
- Ready to implement containerization strategy`,
			),
		),
		Handler: handleAzdDockerGeneration,
	}
}

func handleAzdDockerGeneration(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdDockerGenerationPrompt), nil
}
