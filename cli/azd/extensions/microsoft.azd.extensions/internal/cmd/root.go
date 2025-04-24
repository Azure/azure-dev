// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/spf13/cobra"
)

type rootFlags struct {
	cwd string
}

func NewRootCommand() *cobra.Command {
	flags := &rootFlags{}

	rootCmd := &cobra.Command{
		Use:           "x <command> [options]",
		Short:         "Runs azd developer extension commands",
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

	rootCmd.PersistentFlags().StringVar(
		&flags.cwd,
		"cwd", ".",
		"Path to the azd extension project",
	)

	return rootCmd
}
