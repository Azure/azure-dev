// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"io"
	"log"
	"os"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	// Build the root command using the SDK helper so the extension picks up
	// azd's standard persistent flags (--debug, --no-prompt, -C/--cwd,
	// -e/--environment, -o/--output) and avoids name collisions with azd's
	// reserved global flags. The SDK's --cwd already changes the working
	// directory in PersistentPreRunE, which matches the previous custom flag's
	// purpose of pointing at the extension project directory.
	rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name:  "x",
		Use:   "x <command> [options]",
		Short: "Runs azd developer extension commands",
	})
	sdkPersistentPreRunE := rootCmd.PersistentPreRunE
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if sdkPersistentPreRunE != nil {
			if err := sdkPersistentPreRunE(cmd, args); err != nil {
				return err
			}
		}

		if extCtx.Debug {
			log.SetOutput(os.Stderr)
		} else {
			log.SetOutput(io.Discard)
		}

		return nil
	}

	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.CompletionOptions = cobra.CompletionOptions{
		DisableDefaultCmd: true,
	}

	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})

	rootCmd.AddCommand(newInitCommand(&extCtx.NoPrompt))
	rootCmd.AddCommand(newBuildCommand(&extCtx.OutputFormat))
	rootCmd.AddCommand(newWatchCommand())
	rootCmd.AddCommand(newPackCommand(&extCtx.OutputFormat))
	rootCmd.AddCommand(newReleaseCommand())
	rootCmd.AddCommand(newPublishCommand())
	rootCmd.AddCommand(newVersionCommand())
	rootCmd.AddCommand(newMetadataCommand())

	return rootCmd
}
