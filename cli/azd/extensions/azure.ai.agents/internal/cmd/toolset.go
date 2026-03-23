// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/spf13/cobra"
)

func newToolsetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "toolset",
		Short: "Manage Foundry toolsets.",
		Long: `Manage Foundry toolsets in the current Azure AI Foundry project.

Toolsets are named collections of tools (MCP servers, OpenAPI endpoints, first-party tools)
exposed through a unified MCP-compatible endpoint with platform-managed auth.`,
	}

	cmd.AddCommand(newToolsetListCommand())
	cmd.AddCommand(newToolsetShowCommand())
	cmd.AddCommand(newToolsetCreateCommand())
	cmd.AddCommand(newToolsetDeleteCommand())

	return cmd
}
