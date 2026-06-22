// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name:  "connection",
		Use:   "connection <command> [options]",
		Short: "Manage Microsoft Foundry Connections from your terminal. (Preview)",
	})

	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.CompletionOptions = cobra.CompletionOptions{
		DisableDefaultCmd: true,
	}

	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})

	rootCmd.AddCommand(newContextCommand())
	rootCmd.AddCommand(newVersionCommand(&extCtx.OutputFormat))
	rootCmd.AddCommand(newMetadataCommand(rootCmd))

	// Register the azure.ai.connection service target so `azd up`/`azd deploy`
	// succeed for connection service entries written by `azd ai agent init`.
	rootCmd.AddCommand(azdext.NewListenCommand(configureExtensionHost))

	// Register -p / --project-endpoint as a persistent flag inherited by
	// connection CRUD subcommands (list, show, create, update, delete).
	rootCmd.PersistentFlags().StringP("project-endpoint", "p", "",
		"Foundry project endpoint URL (overrides env var and config)")

	// Connection CRUD subcommands (migrated from the azure.ai.agents extension).
	rootCmd.AddCommand(newConnectionListCommand(extCtx))
	rootCmd.AddCommand(newConnectionShowCommand(extCtx))
	rootCmd.AddCommand(newConnectionCreateCommand(extCtx))
	rootCmd.AddCommand(newConnectionUpdateCommand(extCtx))
	rootCmd.AddCommand(newConnectionDeleteCommand(extCtx))

	return rootCmd
}
