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

// configureExtensionHost is the listen callback. It registers the
// azure.ai.connection service target so `azd up`/`azd deploy` upsert connections
// declared as services in azure.yaml.
func configureExtensionHost(host *azdext.ExtensionHost) {
	azdClient := host.Client()
	host.WithServiceTarget(aiConnectionHost, func() azdext.ServiceTargetProvider {
		return newConnectionServiceTarget(azdClient)
	})
}
