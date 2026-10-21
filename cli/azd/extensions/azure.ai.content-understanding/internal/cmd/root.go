// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name:    "cu",
		Use:     "cu <command> [options]",
		Version: Version,
		Short:   "Work with Azure Content Understanding. (Preview)",
		Long: "Work with Azure Content Understanding using a native Go azd extension.\n\n" +
			"This development build provides the extension foundation. " +
			"Content Understanding operations are not yet available.",
	})

	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.CompletionOptions = cobra.CompletionOptions{
		DisableDefaultCmd: true,
	}

	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})

	rootCmd.AddCommand(newVersionCommand(&extCtx.OutputFormat))
	rootCmd.AddCommand(newMetadataCommand(rootCmd))
	rootCmd.Example = `  # Show extension help
  azd ai cu --help

  # Display the extension version as JSON
  azd ai cu version --output json`

	return rootCmd
}
