// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// NewRootCommand creates the Spring extension's command tree.
func NewRootCommand() *cobra.Command {
	rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name:  "spring",
		Use:   "spring <command> [options]",
		Short: "Prepare a Spring Boot application for deployment to Azure",
	})

	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.CompletionOptions = cobra.CompletionOptions{
		DisableDefaultCmd: true,
	}

	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})

	rootCmd.AddCommand(newInitCommand(extCtx))
	rootCmd.AddCommand(newVersionCommand(&extCtx.OutputFormat))
	rootCmd.AddCommand(newMetadataCommand(rootCmd))

	return rootCmd
}
