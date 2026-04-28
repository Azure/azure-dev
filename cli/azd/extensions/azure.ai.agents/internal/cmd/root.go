// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name:  "agent",
		Use:   "agent <command> [options]",
		Short: fmt.Sprintf("Ship agents with Microsoft Foundry from your terminal. %s", color.YellowString("(Preview)")),
	})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.CompletionOptions.DisableDefaultCmd = true

	// Show the ASCII art banner above the default help text for the root command
	defaultHelp := rootCmd.HelpFunc()
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if cmd == rootCmd {
			printBanner(cmd.OutOrStdout())
		}
		defaultHelp(cmd, args)
	})

	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})

	rootCmd.AddCommand(newListenCommand())
	rootCmd.AddCommand(newVersionCommand())
	rootCmd.AddCommand(newInitCommand(extCtx))
	rootCmd.AddCommand(newRunCommand(extCtx))
	rootCmd.AddCommand(newInvokeCommand(extCtx))
	rootCmd.AddCommand(newMcpCommand())
	rootCmd.AddCommand(newMetadataCommand())
	rootCmd.AddCommand(newShowCommand(extCtx))
	rootCmd.AddCommand(newMonitorCommand(extCtx))
	rootCmd.AddCommand(newFilesCommand(extCtx))
	rootCmd.AddCommand(newSessionCommand(extCtx))

	return rootCmd
}
