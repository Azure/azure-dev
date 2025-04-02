// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	// added for MCP server functionality
	"context"
	"fmt"
	"os/exec"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
)

func newStartCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Get the context of the AZD project & environment.",
		RunE: func(cmd *cobra.Command, args []string) error {
			mcpServer := server.NewMCPServer("azd", "0.0.1",
				server.WithLogging(),
				server.WithInstructions("Provides tools to dynamically run the AZD (Azure Developer CLI) commands."),
			)

			registerTools(mcpServer)

			fmt.Println("Starting MCP server...")
			if err := server.ServeStdio(mcpServer); err != nil {
				return err
			}

			return nil
		},
	}
}

func registerTools(s *server.MCPServer) {
	configShowTool := mcp.NewTool("global-config",
		mcp.WithDescription("Shows the current azd global / user configuration"),
	)

	s.AddTool(configShowTool, showConfig)
}

func showConfig(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cmdResult, err := exec.Command("azd", "config", "show").CombinedOutput()
	if err != nil {
		return nil, err
	}

	return mcp.NewToolResultText(string(cmdResult)), nil
}
