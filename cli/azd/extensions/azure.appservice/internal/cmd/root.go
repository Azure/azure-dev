// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type rootFlagsDefinition struct {
	Debug    bool
	NoPrompt bool
}

func NewRootCommand() *cobra.Command {
	rootCmd, rootFlags := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name:  "appservice",
		Use:   "appservice <command> [options]",
		Short: "Extension for managing Azure App Service resources.",
	})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.CompletionOptions.DisableDefaultCmd = true
	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})

	rootCmd.AddCommand(newSwapCommand(rootFlags))
	rootCmd.AddCommand(newVersionCommand())
	rootCmd.AddCommand(newMetadataCommand())

	return rootCmd
}
