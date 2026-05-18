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

	return rootCmd
}
