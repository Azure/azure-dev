// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/mcp/tools/prompts"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewAzdDiscoveryAnalysisTool creates a new azd discovery analysis tool
func NewAzdDiscoveryAnalysisTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"azd_discovery_analysis",
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription(
				`Provides instructions for scanning and analyzing the current workspace to identify all application components, technologies, dependencies, and communication patterns, documenting findings in the application specification.

This tool returns detailed instructions that the LLM agent should follow using available analysis and documentation tools.

Use this tool when:
- Starting Phase 1 of AZD migration process
- Need to identify all application components and dependencies
- Codebase analysis required before architecture planning
- Application spec does not exist or needs updating`,
			),
		),
		Handler: handleAzdDiscoveryAnalysis,
	}
}

func handleAzdDiscoveryAnalysis(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdDiscoveryAnalysisPrompt), nil
}
