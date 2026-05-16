// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	conncmd "azureaiagent/internal/connections/cmd"

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

	rootCmd.AddCommand(azdext.NewListenCommand(configureExtensionHost))
	rootCmd.AddCommand(newVersionCommand())
	rootCmd.AddCommand(newInitCommand(extCtx))
	rootCmd.AddCommand(newRunCommand(extCtx))
	rootCmd.AddCommand(newInvokeCommand(extCtx))
	rootCmd.AddCommand(newMcpCommand())
	rootCmd.AddCommand(azdext.NewMetadataCommand("1.0", "azure.ai.agents", func() *cobra.Command {
		return rootCmd
	}))
	rootCmd.AddCommand(newShowCommand(extCtx))
	rootCmd.AddCommand(newMonitorCommand(extCtx))
	rootCmd.AddCommand(newFilesCommand(extCtx))
	rootCmd.AddCommand(newSessionCommand(extCtx))
	rootCmd.AddCommand(newProjectCommand(extCtx))

	// Connection commands — in separate package for easy lift-and-shift later.
	// When the azd core namespace change lands, move this AddCommand call
	// to the new root and update the import path.
	rootCmd.AddCommand(conncmd.NewConnectionRootCommand(extCtx))

	return rootCmd
}
