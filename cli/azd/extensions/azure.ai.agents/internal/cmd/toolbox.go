// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/spf13/cobra"
)

func newToolboxCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "toolbox",
		Short: "Manage Foundry toolboxes.",
		Long: `Manage Foundry toolboxes in the current Azure AI Foundry project.

Toolboxes are named collections of tools (MCP servers, OpenAPI endpoints, first-party tools)
exposed through a unified MCP-compatible endpoint with platform-managed auth.`,
	}

	cmd.AddCommand(newToolboxListCommand())
	cmd.AddCommand(newToolboxShowCommand())
	cmd.AddCommand(newToolboxCreateCommand())
	cmd.AddCommand(newToolboxDeleteCommand())

	return cmd
}
