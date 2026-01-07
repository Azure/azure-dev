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

// Enable access to the global command flags
var rootFlags rootFlagsDefinition

func NewRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "finetuning <command> [options]",
		Short:         "Extension for Foundry Fine Tuning. (Preview)",
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

	// Adds support for `--no-prompt` global flag in azd.
	// Without this the extension command will error when the flag is provided.
	rootCmd.PersistentFlags().BoolVar(
		&rootFlags.NoPrompt,
		"no-prompt",
		false,
		"accepts the default value instead of prompting, or fails if there is no default",
	)

	// rootCmd.AddCommand(newListenCommand())
	rootCmd.AddCommand(newVersionCommand())
	rootCmd.AddCommand(newInitCommand(rootFlags))
	rootCmd.AddCommand(newOperationCommand())

	return rootCmd
}
