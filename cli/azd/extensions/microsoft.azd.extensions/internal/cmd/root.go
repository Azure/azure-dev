// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "azd demo <command> [options]",
		Short:         "Demonstrates AZD extension framework capabilities.",
		SilenceUsage:  true,
		SilenceErrors: true,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
	}

	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	rootCmd.PersistentFlags().Bool("debug", false, "Enable debug mode")

	rootCmd.AddCommand(newInitCommand())
	rootCmd.AddCommand(newBuildCommand())
	rootCmd.AddCommand(newWatchCommand())
	rootCmd.AddCommand(newPackCommand())
	rootCmd.AddCommand(newReleaseCommand())
	rootCmd.AddCommand(newPublishCommand())
	rootCmd.AddCommand(newVersionCommand())

	return rootCmd
}
