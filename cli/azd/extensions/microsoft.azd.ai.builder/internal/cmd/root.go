// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.ai.builder/internal/cmd/start"
	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "azd ai builder <command> [options]",
		Short:         "Runs the Azure AI Builder.",
		SilenceUsage:  true,
		SilenceErrors: true,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
	}

	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	rootCmd.PersistentFlags().Bool("debug", false, "Enable debug mode")

	rootCmd.AddCommand(start.NewStartCommand())
	rootCmd.AddCommand(newVersionCommand())

	return rootCmd
}
