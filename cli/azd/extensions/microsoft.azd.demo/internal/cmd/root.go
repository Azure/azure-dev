// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	// Build the root command using the SDK helper so the extension picks up
	// azd's standard persistent flags (--debug, --no-prompt, -C/--cwd,
	// -e/--environment, -o/--output) and the azd-sdk-root annotation without
	// hand-rolling them, avoiding collisions with azd's reserved global flags.
	rootCmd, _ := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name:  "demo",
		Use:   "azd demo <command> [options]",
		Short: "Demonstrates azd extension framework capabilities.",
	})

	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.CompletionOptions = cobra.CompletionOptions{
		DisableDefaultCmd: true,
	}

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
