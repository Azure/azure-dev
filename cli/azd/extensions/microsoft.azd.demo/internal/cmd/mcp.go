// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
)

func newMcpCommand() *cobra.Command {
	mcpCmd := &cobra.Command{
		Use:   "mcp",
		Short: "MCP server commands for demo extension",
	}

	mcpCmd.AddCommand(newMcpStartCommand())

	return mcpCmd
}

func newMcpStartCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start MCP server with demo tools",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMcpServer(cmd.Context())
		},
	}
}

func runMcpServer(ctx context.Context) error {
	// Create MCP server
	s := server.NewMCPServer(
		"AZD Demo Extension MCP Server", "1.0.0",
		server.WithToolCapabilities(true),
	)

	// Add demo tools
	demoTools := []server.ServerTool{
		newGreetingTool(),
		newAzdInfoTool(),
		newSamplingTool(),
		newElicitationTool(),
	}

	s.AddTools(demoTools...)

	// Start the server using stdio transport
	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		return err
	}

	return nil
}

// newGreetingTool creates a simple greeting tool for testing
func newGreetingTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"greeting",
			mcp.WithDescription("A simple greeting tool that takes a name and returns a personalized greeting"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithString("name",
				mcp.Description("The name of the person to greet"),
				mcp.Required(),
			),
		),
		Handler: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Cast arguments to map
			args, ok := request.Params.Arguments.(map[string]interface{})
			if !ok {
				return mcp.NewToolResultError("Invalid arguments format"), nil
			}

			name, ok := args["name"].(string)
			if !ok || name == "" {
				return mcp.NewToolResultError("name parameter is required and must be a string"), nil
			}

			greeting := fmt.Sprintf("Hello, %s! Welcome to the AZD Demo Extension MCP server!", name)
			return mcp.NewToolResultText(greeting), nil
		},
	}
}

// newAzdInfoTool creates a tool that demonstrates access to AZD context using the azd client
func newAzdInfoTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"info",
			mcp.WithDescription("Gets AZD project and environment information using the azd client"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
		),
		Handler: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var info []string
			info = append(info, "=== AZD Demo Extension - Context Information ===")

			// Create a new context that includes the AZD access token
			ctx = azdext.WithAccessToken(ctx)

			// Create a new AZD client
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				info = append(info, fmt.Sprintf("❌ Failed to create AZD client: %v", err))
				return mcp.NewToolResultText(strings.Join(info, "\n")), nil
			}
			defer azdClient.Close()

			// Get project information
			getProjectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
			if err == nil {
				info = append(info, "\n📂 Project Information:")
				info = append(info, fmt.Sprintf("  Name: %s", getProjectResponse.Project.Name))
				info = append(info, fmt.Sprintf("  Path: %s", getProjectResponse.Project.Path))
			} else {
				info = append(info, "\n❌ No azd project found in current directory")
			}

			// Get current environment
			getCurrentEnvResponse, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
			if err == nil {
				currentEnvName := getCurrentEnvResponse.Environment.Name
				info = append(info, "\n🌍 Environment Information:")
				info = append(info, fmt.Sprintf("  Current Environment: %s", currentEnvName))

				// Get environment values
				getValuesResponse, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
					Name: currentEnvName,
				})
				if err == nil && len(getValuesResponse.KeyValues) > 0 {
					info = append(info, "  Environment Variables:")
					for _, pair := range getValuesResponse.KeyValues {
						// Truncate long values for display
						value := pair.Value
						if len(value) > 50 {
							value = value[:47] + "..."
						}
						info = append(info, fmt.Sprintf("    %s: %s", pair.Key, value))
					}
				}
			} else {
				info = append(info, "\n❌ No azd environment found")
			}

			info = append(info, "\n🔧 Extension Information:")
			info = append(info, "  Extension ID: microsoft.azd.demo")
			info = append(info, "  MCP Server: Active")
			info = append(
				info,
				"  Available Tools: greeting, azd_info, sampling, elicitation",
			)

			return mcp.NewToolResultText(strings.Join(info, "\n")), nil
		},
	}
}

// newSamplingTool creates a simple tool that demonstrates MCP sampling
func newSamplingTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"sampling",
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription("Demonstrates MCP sampling by asking the AI to answer a simple question"),
			mcp.WithString("question",
				mcp.Description("The question to ask the AI via sampling"),
				mcp.Required(),
			),
		),
		Handler: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Cast arguments to map
			args, ok := request.Params.Arguments.(map[string]interface{})
			if !ok {
				return mcp.NewToolResultError("Invalid arguments format"), nil
			}

			question, ok := args["question"].(string)
			if !ok || question == "" {
				return mcp.NewToolResultError("question parameter is required"), nil
			}

			// Get the server from context for sampling
			serverFromCtx := server.ServerFromContext(ctx)
			if serverFromCtx == nil {
				return mcp.NewToolResultError("Server not available in context"), nil
			}

			// Create sampling request
			samplingRequest := mcp.CreateMessageRequest{
				CreateMessageParams: mcp.CreateMessageParams{
					Messages: []mcp.SamplingMessage{
						{
							Role: mcp.RoleUser,
							Content: mcp.TextContent{
								Type: "text",
								Text: question,
							},
						},
					},
					SystemPrompt: "You are a helpful assistant. Keep your response brief and clear.",
					MaxTokens:    500,
					Temperature:  0.7,
				},
			}

			// Request sampling from the AI
			samplingResult, err := serverFromCtx.RequestSampling(ctx, samplingRequest)
			if err != nil {
				return mcp.NewToolResultErrorFromErr("Error during sampling", err), nil
			}

			// Extract and return the response
			if textContent, ok := samplingResult.Content.(mcp.TextContent); ok {
				response := fmt.Sprintf(
					"🤖 AI Response via MCP Sampling:\n%s\n\n📝 Original Question: %s",
					textContent.Text,
					question,
				)
				return mcp.NewToolResultText(response), nil
			}

			return mcp.NewToolResultText(fmt.Sprintf("Sampling result: %v", samplingResult.Content)), nil
		},
	}
}

// newElicitationTool creates a simple tool that demonstrates MCP elicitation
func newElicitationTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"elicitation",
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription("Demonstrates MCP elicitation by requesting structured user information"),
			mcp.WithString("prompt_message",
				mcp.Description("Custom message to show when requesting user information (optional)"),
			),
		),
		Handler: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Cast arguments to map
			args, ok := request.Params.Arguments.(map[string]interface{})
			if !ok {
				return mcp.NewToolResultError("Invalid arguments format"), nil
			}

			promptMessage := "Can we learn some more information about you for this AZD demo?"
			if msg, exists := args["prompt_message"]; exists {
				if msgStr, ok := msg.(string); ok && msgStr != "" {
					promptMessage = msgStr
				}
			}

			// Get the server from context for elicitation
			serverFromCtx := server.ServerFromContext(ctx)
			if serverFromCtx == nil {
				return mcp.NewToolResultError("Server not available in context"), nil
			}

			// Create elicitation request with structured schema
			elicitationRequest := mcp.ElicitationRequest{
				Params: mcp.ElicitationParams{
					Message: promptMessage,
					RequestedSchema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name": map[string]any{
								"type":        "string",
								"title":       "Name",
								"description": "Your name",
							},
							"role": map[string]any{
								"type":        "string",
								"title":       "Job Title",
								"description": "Your role or job title",
							},
							"experience_level": map[string]any{
								"type":        "string",
								"title":       "Level of experience",
								"description": "Your experience level with Azure",
								"enum":        []string{"Beginner", "Intermediate", "Advanced", "Expert"},
							},
							"favorite_language": map[string]any{
								"type":        "string",
								"Title":       "Favorite programming language",
								"description": "Your favorite programming language",
								"enum": []string{
									"Go",
									"Python",
									"JavaScript",
									"TypeScript",
									"Java",
									"C#",
									"C++",
									"Rust",
									"Other",
								},
							},
							"project_type": map[string]any{
								"type":        "string",
								"title":       "Project type",
								"description": "Type of project you're working on",
								"enum": []string{
									"Web App",
									"API",
									"Microservices",
									"Mobile",
									"Desktop",
									"AI/ML",
									"Other",
								},
							},
							"team_size": map[string]any{
								"type":        "integer",
								"title":       "Team size",
								"description": "Size of your development team",
								"minimum":     1,
								"maximum":     1000,
							},
						},
						"required": []string{"name"},
					},
				},
			}

			// Request elicitation from the user
			elicitationResult, err := serverFromCtx.RequestElicitation(ctx, elicitationRequest)
			if err != nil {
				return mcp.NewToolResultErrorFromErr("Error during elicitation", err), nil
			}

			elicitationData, ok := elicitationResult.Content.(map[string]any)
			if !ok {
				return mcp.NewToolResultError("data not in expected format"), nil
			}

			// Format and return the collected information
			response := fmt.Sprintf(
				"🔍 MCP Elicitation Demo - Information Collected\n"+
					"════════════════════════════════════════════════\n\n"+
					"� User Information:\n%s\n\n"+
					"✅ Elicitation successful! This demonstrates how MCP can collect structured "+
					"user data using predefined schemas.",
				formatElicitationData(elicitationData),
			)

			return mcp.NewToolResultText(response), nil
		},
	}
}

// formatElicitationData formats the elicitation result data for display
func formatElicitationData(data map[string]any) string {
	if len(data) == 0 {
		return "  No information provided"
	}

	var lines []string
	if name, ok := data["name"].(string); ok {
		lines = append(lines, fmt.Sprintf("  👤 Name: %s", name))
	}
	if role, ok := data["role"].(string); ok {
		lines = append(lines, fmt.Sprintf("  💼 Role: %s", role))
	}
	if exp, ok := data["experience_level"].(string); ok {
		lines = append(lines, fmt.Sprintf("  🎯 Azure Experience: %s", exp))
	}
	if lang, ok := data["favorite_language"].(string); ok {
		lines = append(lines, fmt.Sprintf("  💻 Favorite Language: %s", lang))
	}
	if proj, ok := data["project_type"].(string); ok {
		lines = append(lines, fmt.Sprintf("  🚀 Project Type: %s", proj))
	}
	if size, ok := data["team_size"].(float64); ok {
		lines = append(lines, fmt.Sprintf("  👥 Team Size: %.0f", size))
	}

	if len(lines) == 0 {
		return "  No valid information provided"
	}

	return strings.Join(lines, "\n")
}
