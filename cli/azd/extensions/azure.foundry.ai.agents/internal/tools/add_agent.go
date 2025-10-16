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

// NewAddAgentTool creates a tool for adding AI Foundry agents to a project
func NewAddAgentTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"add_agent",
			mcp.WithDescription("Adds an AI Foundry agent service to the current azd project."),
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithString("agent_name",
				mcp.Description("The name of the agent that will be used to reference the new agent within the services section of the azure.yaml"),
				mcp.DefaultString("my-agent"),
				mcp.Required(),
			),
			mcp.WithString("manifest_location",
				mcp.Description("The relative file path or URL to the agent YAML manifest."),
				mcp.Required(),
			),
			mcp.WithString("source_code_location",
				mcp.Description("The relative file path to the agent source code from the project root."),
			),
			mcp.WithString("language",
				mcp.Description("The programming language of the agent source code."),
				mcp.WithStringEnumItems([]string{"csharp", "python", "javascript", "typescript", "java", "docker", "custom"}),
				mcp.DefaultString("python"),
			),
		),
		Handler: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			agentName, err := request.RequireString("agent_name")
			if err != nil {
				return mcp.NewToolResultErrorFromErr("The agent_name is parameter is required and cannot be empty. The agent_name is used to uniquely identify the agent within the services section of the azure.yaml", err), nil
			}

			if _, err := request.RequireString("manifest_location"); err != nil {
				return mcp.NewToolResultErrorFromErr("The manifest_location is required and cannot be empty. The manifest_location specifies the location of the agent's YAML manifest file.", err), nil
			}

			sourceCodeLocation := request.GetString("source_code_location", ".")
			programmingLanguage := request.GetString("language", "custom")

			// Create a new context that includes the AZD access token
			ctx = azdext.WithAccessToken(ctx)

			// Create a new AZD client
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Failed to create AZD client: %v", err)), nil
			}
			defer azdClient.Close()

			// Verify we have a project
			projectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
			if err != nil {
				return mcp.NewToolResultError("No azd project found in current directory. Please run 'azd init' first."), nil
			}

			// TODO: Call into same code from init to init the agent from manifest.

			if _, has := projectResponse.Project.Services[agentName]; has {
				return mcp.NewToolResultError(fmt.Sprintf("A service with name '%s' already exists.")), nil
			}

			agentService := azdext.ServiceConfig{
				Name:         agentName,
				RelativePath: sourceCodeLocation,
				Host:         "foundry.containeragent",
				Language:     programmingLanguage,
			}

			_, err = azdClient.
				Project().
				AddService(ctx, &azdext.AddServiceRequest{
					Service: &agentService,
				})

			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			result := map[string]any{
				"service": map[string]any{
					"name":         agentName,
					"relativePath": sourceCodeLocation,
					"host":         "foundry.containerAgent",
					"language":     programmingLanguage,
				},
				"success": true,
			}

			return mcp.NewToolResultStructured(result, fmt.Sprintf("Service '%s' created successfully", agentName)), nil
		},
	}
}
