// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// NewRootCommand builds the cobra root command for the azure.ai.inspector
// extension. The root command groups inspector subcommands; launch is the
// user-facing command that starts the inspector.
func NewRootCommand() *cobra.Command {
	rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name:  "inspector",
		Use:   "inspector <command> [options]",
		Short: "Inspect locally running Foundry agents in a browser. (Preview)",
	})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.CompletionOptions.DisableDefaultCmd = true

	// Configure debug logging once on the root command so every subcommand
	// inherits it. Without this, the inspector's verbose proxy and SSE
	// traffic logs (gated on log.Default()) would surface as user-facing
	// stderr noise. cobra.EnableTraverseRunHooks (set by the SDK) ensures
	// this runs alongside any subcommand pre-runs.
	var closeDebugLog func() error
	sdkPreRun := rootCmd.PersistentPreRunE
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if sdkPreRun != nil {
			if err := sdkPreRun(cmd, args); err != nil {
				return err
			}
		}
		closeDebugLog = setupDebugLogging(cmd.Flags())
		return nil
	}
	sdkPostRun := rootCmd.PersistentPostRunE
	rootCmd.PersistentPostRunE = func(cmd *cobra.Command, args []string) error {
		var runErr error
		if sdkPostRun != nil {
			runErr = sdkPostRun(cmd, args)
		}
		if closeDebugLog != nil {
			if err := closeDebugLog(); runErr == nil {
				runErr = err
			}
			closeDebugLog = nil
		}
		return runErr
	}

	rootCmd.AddCommand(newLaunchCommand())
	rootCmd.AddCommand(azdext.NewListenCommand(nil))
	rootCmd.AddCommand(azdext.NewMetadataCommand("1.0", "azure.ai.inspector", func() *cobra.Command {
		return rootCmd
	}))
	rootCmd.AddCommand(newVersionCommand(&extCtx.OutputFormat))

	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})

	return rootCmd
}
