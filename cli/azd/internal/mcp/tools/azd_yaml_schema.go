// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/mcp/tools/prompts"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewAzdYamlSchemaTool creates a new azd yaml schema tool
func NewAzdYamlSchemaTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"azd_yaml_schema",
			mcp.WithDescription(
				`Gets the Azure YAML JSON schema file specification and structure for azure.yaml `+
					`configuration files used in AZD.`,
			),
		),
		Handler: handleAzdYamlSchema,
	}
}

func handleAzdYamlSchema(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(prompts.AzdYamlSchemaPrompt), nil
}
