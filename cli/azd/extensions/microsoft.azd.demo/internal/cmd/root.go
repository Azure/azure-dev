// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "azd demo <command> [options]",
		Short:         "Demonstrates azd extension framework capabilities.",
		SilenceUsage:  true,
		SilenceErrors: true,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
		Annotations: map[string]string{
			"azd-sdk-root": "true",
		},
	}

	// Register reserved azd flags so that `listen --debug` doesn't fail.
	flags := rootCmd.PersistentFlags()
	flags.Bool("debug", false, "Enables debug and diagnostics logging")
	flags.Bool("no-prompt", false, "Runs without prompts")

	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})

	rootCmd.AddCommand(newListenCommand())
	rootCmd.AddCommand(newContextCommand())
	rootCmd.AddCommand(newPromptCommand())
	rootCmd.AddCommand(newColorsCommand())
	rootCmd.AddCommand(newVersionCommand())
	rootCmd.AddCommand(newMcpCommand())
	rootCmd.AddCommand(newConfigCommand())
	rootCmd.AddCommand(newGhUrlParseCommand())
	rootCmd.AddCommand(newMetadataCommand())
	rootCmd.AddCommand(newAiCommand())
	rootCmd.AddCommand(newCopilotCommand())

	return rootCmd
}
