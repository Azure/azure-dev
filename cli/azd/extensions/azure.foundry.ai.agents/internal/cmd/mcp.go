// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"

	"azureaiagent/internal/tools"

	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
)

func newMcpCommand() *cobra.Command {
	mcpCmd := &cobra.Command{
		Use:   "mcp",
		Short: "MCP server commands for AI Foundry agents extension",
	}

	mcpCmd.AddCommand(newMcpStartCommand())

	return mcpCmd
}

func newMcpStartCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start MCP server with AI Foundry agent tools",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMcpServer(cmd.Context())
		},
	}
}

func runMcpServer(ctx context.Context) error {
	// Create MCP server
	s := server.NewMCPServer(
		"AZD AI Foundry Agents Extension MCP Server", "1.0.0",
		server.WithToolCapabilities(true),
		server.WithElicitation(),
	)

	s.EnableSampling()

	// Add AI Foundry agent tools
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
