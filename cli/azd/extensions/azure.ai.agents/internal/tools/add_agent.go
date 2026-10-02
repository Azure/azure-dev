// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewAddAgentTool creates a non-mutating guide for adding Microsoft Foundry
// agents from a unified azure.yaml.
func NewAddAgentTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"add_agent",
			mcp.WithDescription(
				"Guide initialization of a Microsoft Foundry agent from a unified azure.yaml; this tool does not modify files",
			),
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithString("azure_yaml_location",
				mcp.Description("The file path or URL to a unified azure.yaml project document"),
				mcp.Required(),
			),
		),
		Handler: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Cast arguments to map
			args, ok := request.Params.Arguments.(map[string]any)
			if !ok {
				return mcp.NewToolResultError("Invalid arguments format"), nil
			}

			azureYamlLocation, ok := args["azure_yaml_location"].(string)
			if !ok || azureYamlLocation == "" {
				return mcp.NewToolResultError(
					"azure_yaml_location parameter is required and must be a string",
				), nil
			}

			// Create a new context that includes the azd access token
			ctx = azdext.WithAccessToken(ctx)

			// Create a new azd client
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Failed to create azd client: %v", err)), nil
			}
			defer azdClient.Close()

			// Verify we have a project
			_, err = azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
			if err != nil {
				return mcp.NewToolResultError("No azd project found in current directory. Please run 'azd init' first."), nil
			}

			result := unifiedInitGuidance(azureYamlLocation)

			return mcp.NewToolResultText(result), nil
		},
	}
}

func unifiedInitGuidance(azureYamlLocation string) string {
	return fmt.Sprintf(
		"No files or resources were changed. To initialize this project from the unified azure.yaml, run:\n\n"+
			"azd ai agent init -m %q",
		azureYamlLocation,
	)
}
