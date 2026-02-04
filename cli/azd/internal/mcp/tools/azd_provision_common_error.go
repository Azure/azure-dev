// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/mcp/tools/prompts"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewAzdProvisionCommonErrorTool creates a new azd provision common error tool
func NewAzdProvisionCommonErrorTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"provision_common_error",
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription(
				`Returns specific troubleshooting instructions for common Azure Developer CLI (azd) provisioning errors.

Use this tool when azd commands fail with:
- Service unavailability errors (quota/capacity issues)
- Authorization failures for role assignments
- Role assignment conflicts (already exists)
- Location offer restricted errors for Azure Database for PostgreSQL
- VM quota exceeded errors when provisioning compute resources
- Cognitive Services account provisioning state invalid errors

Provides step-by-step diagnostic instructions for the LLM agent to execute.`,
			),
		),
		Handler: handleAzdProvisionCommonError,
	}
}

func handleAzdProvisionCommonError(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdProvisionCommonErrorPrompt), nil
}
