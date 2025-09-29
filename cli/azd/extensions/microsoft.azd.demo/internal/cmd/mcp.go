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
		newDemoGreetingTool(),
		newDemoCalculatorTool(),
		newDemoAzdInfoTool(),
	}

	s.AddTools(demoTools...)

	// Start the server using stdio transport
	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		return err
	}

	return nil
}

// newDemoGreetingTool creates a simple greeting tool for testing
func newDemoGreetingTool() server.ServerTool {
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

// newDemoCalculatorTool creates a simple calculator tool for testing
func newDemoCalculatorTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"calculator",
			mcp.WithDescription(
				"A simple calculator that can perform basic arithmetic operations (add, subtract, multiply, divide)",
			),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithString("operation",
				mcp.Description("The operation to perform: add, subtract, multiply, divide"),
				mcp.Required(),
			),
			mcp.WithNumber("a",
				mcp.Description("First number"),
				mcp.Required(),
			),
			mcp.WithNumber("b",
				mcp.Description("Second number"),
				mcp.Required(),
			),
		),
		Handler: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Cast arguments to map
			args, ok := request.Params.Arguments.(map[string]interface{})
			if !ok {
				return mcp.NewToolResultError("Invalid arguments format"), nil
			}

			operation, ok := args["operation"].(string)
			if !ok {
				return mcp.NewToolResultError("operation parameter is required and must be a string"), nil
			}

			a, ok := args["a"].(float64)
			if !ok {
				return mcp.NewToolResultError("'a' parameter is required and must be a number"), nil
			}

			b, ok := args["b"].(float64)
			if !ok {
				return mcp.NewToolResultError("'b' parameter is required and must be a number"), nil
			}

			var result float64
			var opSymbol string

			switch operation {
			case "add":
				result = a + b
				opSymbol = "+"
			case "subtract":
				result = a - b
				opSymbol = "-"
			case "multiply":
				result = a * b
				opSymbol = "*"
			case "divide":
				if b == 0 {
					return mcp.NewToolResultError("Division by zero is not allowed"), nil
				}
				result = a / b
				opSymbol = "/"
			default:
				return mcp.NewToolResultError(
					fmt.Sprintf("Unknown operation '%s'. Supported operations: add, subtract, multiply, divide", operation),
				), nil
			}

			response := fmt.Sprintf("%.2f %s %.2f = %.2f", a, opSymbol, b, result)
			return mcp.NewToolResultText(response), nil
		},
	}
}

// newDemoAzdInfoTool creates a tool that demonstrates access to AZD context using the azd client
func newDemoAzdInfoTool() server.ServerTool {
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
			info = append(info, "  Available Tools: demo_greeting, demo_calculator, demo_azd_info")

			return mcp.NewToolResultText(strings.Join(info, "\n")), nil
		},
	}
}
