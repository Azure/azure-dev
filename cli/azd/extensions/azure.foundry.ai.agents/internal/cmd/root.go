// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "agent <command> [options]",
		Short:         "Placeholder description",
		SilenceUsage:  true,
		SilenceErrors: true,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
	}

	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	rootCmd.PersistentFlags().Bool("debug", false, "Enable debug mode")

	rootCmd.AddCommand(newListenCommand())
	rootCmd.AddCommand(newVersionCommand())
	rootCmd.AddCommand(newInitCommand())
	rootCmd.AddCommand(newDeployCommand())
	rootCmd.AddCommand(newMcpCommand())

	return rootCmd
}
