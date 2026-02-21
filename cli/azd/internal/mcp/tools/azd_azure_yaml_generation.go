// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/mcp/tools/prompts"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewAzdAzureYamlGenerationTool creates a new azd azure yaml generation tool
func NewAzdAzureYamlGenerationTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"azure_yaml_generation",
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription(
				`Returns instructions for generating the azure.yaml configuration file with proper service hosting, 
build, and deployment settings for azd projects. 

The LLM agent should execute these instructions using available tools.

Use this tool when:
- Architecture planning has been completed and Azure services selected
- Need to create or update azure.yaml configuration file
- Services have been mapped to Azure hosting platforms
- Ready to define build and deployment configurations`,
			),
		),
		Handler: handleAzdAzureYamlGeneration,
	}
}

func handleAzdAzureYamlGeneration(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdAzureYamlGenerationPrompt), nil
}
