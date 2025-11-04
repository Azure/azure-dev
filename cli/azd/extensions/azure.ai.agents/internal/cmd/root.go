// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/spf13/cobra"
)

type rootFlagsDefinition struct {
	Debug    bool
	NoPrompt bool
}

var rootFlags rootFlagsDefinition

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
	rootCmd.PersistentFlags().BoolVar(
		&rootFlags.Debug,
		"debug",
		false,
		"Enable debug mode",
	)
	rootCmd.PersistentFlags().BoolVar(
		&rootFlags.NoPrompt,
		"no-prompt",
		false,
		"Accepts the default value instead of prompting, or it fails if there is no default.",
	)

	rootCmd.AddCommand(newListenCommand())
	rootCmd.AddCommand(newVersionCommand())
	rootCmd.AddCommand(newInitCommand())
	rootCmd.AddCommand(newDeployCommand())
	rootCmd.AddCommand(newMcpCommand())

	return rootCmd
}
