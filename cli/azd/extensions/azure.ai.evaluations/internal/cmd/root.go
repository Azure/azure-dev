// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"azureaieval/internal/foundry/projectctx"
	"azureaieval/internal/version"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// NewRootCommand builds the `azd ai eval` command tree.
func NewRootCommand() *cobra.Command {
	rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name: "eval",
		Use:  "eval <command> [options]",
		Short: fmt.Sprintf(
			"Define and run Foundry evaluations from your terminal. %s",
			color.YellowString("(Beta)"),
		),
	})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.CompletionOptions.DisableDefaultCmd = true

	// The data-plane clients trace requests through the standard logger, which
	// Go writes to stderr, so it has to be silenced unless debug was asked for.
	//
	// The SDK's own hook is chained rather than replaced, and cobra ignores
	// PersistentPreRun entirely once PersistentPreRunE is set. The SDK sets
	// cobra.EnableTraverseRunHooks, so this still runs alongside subcommand
	// hooks. The cleanup func is discarded on purpose: log writes are
	// unbuffered and the OS closes the file at exit.
	sdkPreRun := rootCmd.PersistentPreRunE
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if sdkPreRun != nil {
			if err := sdkPreRun(cmd, args); err != nil {
				return err
			}
		}
		// -e/--environment is parsed by the SDK into extCtx and then has to be
		// acted on. Discarding extCtx left the flag accepted and ignored:
		// `azd ai eval create -e staging` read the endpoint out of the default
		// environment and wrote its eval id back there, and even a name azd
		// itself rejects was accepted in silence. Set here rather than at each
		// reader, so there is one answer to which environment this invocation
		// is about.
		cmd.SetContext(projectctx.WithSelectedEnvironment(cmd.Context(), extCtx.Environment))
		if err := projectctx.VerifySelectedEnvironment(cmd.Context()); err != nil {
			return err
		}
		setupDebugLogging(cmd.Flags())
		return nil
	}

	rootCmd.AddCommand(
		newInitCommand(),
		newDatasetCommand(),
		newRunCommand(),
		newEvaluatorCommand(),
		newGenerateCommand(),
		newJobCommand(),
		newEvalCreateCommand(),
		newEvalListCommand(),
		newEvalShowCommand(),
		newEvalDeleteCommand(),
		newListenCommand(),
	)

	// The shared release stage validates each published artifact by running
	// `<binary> version`, so an extension without this command fails the bundle.
	rootCmd.AddCommand(azdext.NewVersionCommand(
		"azure.ai.evaluations", version.Version, &extCtx.OutputFormat))

	// The manifest declares the `metadata` capability, which azd uses to
	// discover this extension's command tree. Without the command registered,
	// that discovery fails with "unknown command".
	rootCmd.AddCommand(azdext.NewMetadataCommand("1.0", "azure.ai.evaluations", func() *cobra.Command {
		return rootCmd
	}))

	return rootCmd
}
