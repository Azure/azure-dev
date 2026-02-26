// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"

	"azureaiagent/internal/tools"

	"github.com/fatih/color"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
)

func newMcpCommand() *cobra.Command {
	mcpCmd := &cobra.Command{
		Use:    "mcp",
		Short:  fmt.Sprintf("MCP server commands for Microsoft Foundry agents extension. %s", color.YellowString("(Preview)")),
		Hidden: true,
	}

	mcpCmd.AddCommand(newMcpStartCommand())

	return mcpCmd
}

func newMcpStartCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: fmt.Sprintf("Start MCP server with Microsoft Foundry agent tools. %s", color.YellowString("(Preview)")),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMcpServer(cmd.Context())
		},
	}
}

func runMcpServer(ctx context.Context) error {
	// Create MCP server
	s := server.NewMCPServer(
		"azd Microsoft Foundry Agents Extension MCP Server", "1.0.0",
		server.WithToolCapabilities(true),
		server.WithElicitation(),
	)

	s.EnableSampling()

	// Add Microsoft Foundry agent tools
	agentTools := []server.ServerTool{
		tools.NewAddAgentTool(),
	}

	s.AddTools(agentTools...)

	// Start the server using stdio transport
	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		return err
	}

	return nil
}
