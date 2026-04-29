// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name:  "models",
		Use:   "models <command> [options]",
		Short: "Extension for managing custom models in Azure AI Foundry. (Preview)",
	})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.CompletionOptions.DisableDefaultCmd = true

	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})

	rootCmd.AddCommand(newVersionCommand())
	rootCmd.AddCommand(newMetadataCommand())
	rootCmd.AddCommand(newInitCommand(extCtx))
	rootCmd.AddCommand(newCustomCommand(extCtx))

	return rootCmd
}
