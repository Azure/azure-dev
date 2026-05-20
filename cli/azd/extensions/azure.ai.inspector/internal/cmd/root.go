// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// NewRootCommand builds the cobra root command for the azure.ai.inspector
// extension. The root command itself launches the inspector; subcommands
// provide standard extension plumbing (listen, metadata, version).
func NewRootCommand() *cobra.Command {
	rootCmd, _ := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name:  "inspector",
		Use:   "inspector [options]",
		Short: "Launch the Foundry agent inspector UI in a browser. (Preview)",
	})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.CompletionOptions.DisableDefaultCmd = true

	// Configure debug logging once on the root command so every subcommand
	// inherits it. Without this, the inspector's verbose proxy and SSE
	// traffic logs (gated on log.Default()) would surface as user-facing
	// stderr noise. cobra.EnableTraverseRunHooks (set by the SDK) ensures
	// this runs alongside any subcommand pre-runs.
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

	// `azd ai inspector` (with no subcommand) launches the inspector. The
	// flag set, RunE, and help text live on the dedicated builder so the
	// root command's behavior is co-located with its flags.
	leaf := newInspectorCommand()
	rootCmd.RunE = leaf.RunE
	rootCmd.Long = leaf.Long
	rootCmd.Example = leaf.Example
	rootCmd.Flags().AddFlagSet(leaf.Flags())

	rootCmd.AddCommand(azdext.NewListenCommand(nil))
	rootCmd.AddCommand(azdext.NewMetadataCommand("1.0", "azure.ai.inspector", func() *cobra.Command {
		return rootCmd
	}))
	rootCmd.AddCommand(newVersionCommand())

	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})

	return rootCmd
}
