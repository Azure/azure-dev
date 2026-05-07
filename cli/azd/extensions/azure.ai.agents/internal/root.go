// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"

	agents "azureaiagent/internal/agents/cmd"
	toolboxes "azureaiagent/internal/toolboxes/cmd"
)

func NewRootCommand() *cobra.Command {
	rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name:  "ai",
		Use:   "ai <command> [options]",
		Short: fmt.Sprintf("Ship agents with Microsoft Foundry from your terminal. %s", color.YellowString("(Preview)")),
	})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.CompletionOptions.DisableDefaultCmd = true

	// Configure debug logging once on the root command so every subcommand
	// inherits it (cobra.EnableTraverseRunHooks, set by the SDK, ensures this
	// runs alongside any subcommand pre-runs). The cleanup func is intentionally
	// discarded: log writes are unbuffered and the OS closes the file on exit.
	sdkPreRun := rootCmd.PersistentPreRunE
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if sdkPreRun != nil {
			if err := sdkPreRun(cmd, args); err != nil {
				return err
			}
		}
		setupDebugLogging(cmd.Flags())
		return nil
	}

	// Show the ASCII art banner above the default help text for the root command
	defaultHelp := rootCmd.HelpFunc()
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if cmd == rootCmd {
			printBanner(cmd.OutOrStdout())
		}
		defaultHelp(cmd, args)
	})

	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})

	rootCmd.AddCommand(agents.NewAgentRootCommand(extCtx))
	rootCmd.AddCommand(toolboxes.NewToolboxesRootCommand(extCtx))

	return rootCmd
}
