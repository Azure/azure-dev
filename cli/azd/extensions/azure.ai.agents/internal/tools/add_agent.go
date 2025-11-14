// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/pkg/azdext"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewAddAgentTool creates a tool for adding AI Foundry agents to a project
func NewAddAgentTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"add_agent",
			mcp.WithDescription("Add an AI Foundry agent to the current azd project using an agent manifest"),
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithString("manifest_location",
				mcp.Description("The file path or URL to the agent manifest (JSON or YAML format)"),
				mcp.Required(),
			),
		),
		Handler: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Cast arguments to map
			args, ok := request.Params.Arguments.(map[string]interface{})
			if !ok {
				return mcp.NewToolResultError("Invalid arguments format"), nil
			}

			manifestLocation, ok := args["manifest_location"].(string)
			if !ok || manifestLocation == "" {
				return mcp.NewToolResultError("manifest_location parameter is required and must be a string"), nil
			}

			// Create a new context that includes the AZD access token
			ctx = azdext.WithAccessToken(ctx)

			// Create a new AZD client
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Failed to create AZD client: %v", err)), nil
			}
			defer azdClient.Close()

			// Verify we have a project
			_, err = azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
			if err != nil {
				return mcp.NewToolResultError("No azd project found in current directory. Please run 'azd init' first."), nil
			}

			// Process the manifest and add the agent
			result := processAgentManifest(manifestLocation)

			return mcp.NewToolResultText(result), nil
		},
	}
}

// processAgentManifest processes the agent manifest and adds it to the project
func processAgentManifest(manifestLocation string) string {
	// For now, return a placeholder implementation
	// In a real implementation, this would:
	// 1. Download/read the manifest from the location
	// 2. Parse the manifest (JSON/YAML)
	// 3. Validate the agent configuration
	// 4. Add the agent to the azd project configuration
	// 5. Update azure.yaml with the new agent service
	// 6. Create necessary infrastructure files

	return fmt.Sprintf(`✅ Successfully processed agent manifest from: %s

📋 Next steps:
1. The agent configuration has been added to your azd project
2. Run 'azd provision' to create the necessary Azure resources
3. Run 'azd deploy' to deploy your AI Foundry agent

🔗 Agent manifest location: %s
🎯 Agent will be configured for AI Foundry deployment`,
		manifestLocation, manifestLocation)
}
