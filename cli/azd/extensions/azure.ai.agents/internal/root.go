// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"

	agentcmd "azureaiagent/internal/cmd"
	connectioncmd "azureaiagent/internal/connections/cmd"
)

// NewRootCommand creates the top-level "ai" root command for the extension.
// It wires agent and connection as sibling subcommand groups under "azd ai".
func NewRootCommand() *cobra.Command {
	rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name:  "ai",
		Use:   "ai <command> [options]",
		Short: fmt.Sprintf("Manage agents and connections in Microsoft Foundry. %s", color.YellowString("(Preview)")),
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
		agentcmd.SetupDebugLogging(cmd.Flags())
		return nil
	}

	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})

	// Sibling command groups — each self-contained, easy to extract later
	rootCmd.AddCommand(agentcmd.NewAgentRootCommand(extCtx))
	rootCmd.AddCommand(connectioncmd.NewConnectionRootCommand(extCtx))

	return rootCmd
}
