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
		Use:           "training <command> [options]",
		Short:         "Extension for Azure AI Foundry custom training jobs. (Preview)",
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
		"accepts the default value instead of prompting, or fails if there is no default",
	)

	rootCmd.AddCommand(newVersionCommand())
	rootCmd.AddCommand(newInitCommand(rootFlags))
	rootCmd.AddCommand(newJobCommand())
	rootCmd.AddCommand(newMetadataCommand())

	return rootCmd
}
