// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// NewConnectionRootCommand creates the "connection" subcommand group under "azd ai".
func NewConnectionRootCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "connection <command> [options]",
		Short: "Manage Foundry project connections. (Preview)",
		Long: `Manage connections (connected resources) in a Foundry project.

Connections link a Foundry project to external services such as MCP servers,
AI Search, Bing, ACR, App Insights, AI Services, and custom APIs.

Each connection has a kind, target URL, auth type, optional credentials,
and optional metadata.`,
	}

	// Register -p / --project-endpoint as a persistent flag so all subcommands inherit it
	cmd.PersistentFlags().StringP("project-endpoint", "p", "",
		"Foundry project endpoint URL (overrides env var and config)")

	cmd.AddCommand(newConnectionListCommand(extCtx))
	cmd.AddCommand(newConnectionShowCommand(extCtx))
	cmd.AddCommand(newConnectionCreateCommand(extCtx))
	cmd.AddCommand(newConnectionUpdateCommand(extCtx))
	cmd.AddCommand(newConnectionDeleteCommand(extCtx))

	return cmd
}
