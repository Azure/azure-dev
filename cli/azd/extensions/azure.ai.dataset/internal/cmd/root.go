// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"azureaidataset/internal/foundry/projectctx"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// NewRootCommand builds the `azd ai dataset` command tree.
func NewRootCommand() *cobra.Command {
	rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name: "dataset",
		Use:  "dataset <command> [options]",
		Short: fmt.Sprintf(
			"Register and version Foundry datasets from your terminal. %s",
			color.YellowString("(Beta)"),
		),
	})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.CompletionOptions.DisableDefaultCmd = true

	// The data-plane clients trace requests through the standard logger, which
	// Go writes to stderr, so it has to be silenced unless debug was asked for.
	// The SDK's own hook is chained rather than replaced, and cobra ignores
	// PersistentPreRun entirely once PersistentPreRunE is set.
	sdkPreRun := rootCmd.PersistentPreRunE
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if sdkPreRun != nil {
			if err := sdkPreRun(cmd, args); err != nil {
				return err
			}
		}
		// -e/--environment is parsed by the SDK into extCtx and then has to be
		// acted on. Discarding extCtx left the flag accepted and ignored:
		// `azd ai dataset create -e staging` read the endpoint out of the
		// default environment and wrote its version back there, and even a name
		// azd itself rejects was accepted in silence. Set here rather than at
		// each reader, so there is one answer to which environment this
		// invocation is about.
		cmd.SetContext(projectctx.WithSelectedEnvironment(cmd.Context(), extCtx.Environment))
		setupDebugLogging(cmd.Flags())
		return nil
	}

	// Generation stays with `azure.ai.evaluations`: it writes the `datasets:`
	// entry in evals/eval.yaml, which is that extension's file.
	rootCmd.AddCommand(
		newDatasetCreateCommand(),
		newDatasetUpdateCommand(),
		newDatasetListCommand(),
		newDatasetShowCommand(),
		newDatasetDeleteCommand(),
		newDatasetVersionsCommand(),
	)

	// The manifest declares the `metadata` capability, which azd uses to
	// discover this extension's command tree. Without the command registered,
	// that discovery fails with "unknown command".
	rootCmd.AddCommand(azdext.NewMetadataCommand("1.0", "azure.ai.dataset", func() *cobra.Command {
		return rootCmd
	}))

	return rootCmd
}
