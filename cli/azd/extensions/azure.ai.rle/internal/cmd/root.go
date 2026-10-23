// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

const (
	rleEnableAllEnvVar = "AZD_AI_RLE_ENABLE_ALL"
)

func NewRootCommand() *cobra.Command {
	return newRootCommand(newRegistryExtensionUpdateChecker())
}

func newRootCommand(updateChecker extensionUpdateChecker) *cobra.Command {
	rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name:  "rle",
		Use:   "rle <command> [options]",
		Short: fmt.Sprintf("Manage RLE resources from your terminal. %s", color.YellowString("(Preview)")),
	})

	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.CompletionOptions.DisableDefaultCmd = true
	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	var availableUpdate *extensionUpdate
	sdkPreRun := rootCmd.PersistentPreRunE
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if err := sdkPreRun(cmd, args); err != nil {
			return err
		}
		availableUpdate = nil
		switch cmd.Name() {
		case "metadata", "models", "version":
			return nil
		case "cancel":
			if parent := cmd.Parent(); parent != nil && parent.Name() == "jobs" {
				return nil
			}
		}

		update, err := updateChecker.Check(cmd.Context(), Version)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: unable to check for RLE updates: %v\n", err)
			return nil
		}
		if update == nil {
			return nil
		}
		if update.IsBreaking {
			return breakingUpdateError(update)
		}

		availableUpdate = update
		return nil
	}
	sdkPostRun := rootCmd.PersistentPostRunE
	rootCmd.PersistentPostRunE = func(cmd *cobra.Command, args []string) error {
		if sdkPostRun != nil {
			if err := sdkPostRun(cmd, args); err != nil {
				return err
			}
		}
		if availableUpdate != nil {
			fmt.Fprintln(
				cmd.ErrOrStderr(),
				color.YellowString(
					"RLE extension update available: %s\nTo update, run `%s`",
					availableUpdate.LatestVersion,
					extensionUpdateCommand(availableUpdate.Source),
				),
			)
			fmt.Fprintln(cmd.ErrOrStderr())
		}
		return nil
	}

	defaultHelp := rootCmd.HelpFunc()
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if cmd == rootCmd {
			printBanner(cmd.OutOrStdout())
		}
		defaultHelp(cmd, args)
	})

	userCommands := []*cobra.Command{
		newListCommand(&extCtx.OutputFormat),
		newModelsCommand(&extCtx.OutputFormat),
		newShowCommand(&extCtx.OutputFormat),
		newRolloutCommand(),
		newMonitorCommand(),
		newPublishCommand(),
		newRunCommand(),
	}
	for _, command := range userCommands {
		rootCmd.AddCommand(command)
	}

	// internalCommands are staged for a future release and are only shown to the RLE
	// team's own iteration.
	internalCommands := []*cobra.Command{
		newJobsCommand(&extCtx.OutputFormat),
		newTrainCommand(),
	}
	for _, command := range internalCommands {
		command.Hidden = !rleEnableAllEnabled()
		rootCmd.AddCommand(command)
	}

	rootCmd.AddCommand(newVersionCommand(&extCtx.OutputFormat))
	rootCmd.AddCommand(newMetadataCommand(rootCmd))

	return rootCmd
}

// rleEnableAllEnabled reports whether experimental training commands should be
// shown in help. Intended for the RLE team's own iteration, not for general use.
func rleEnableAllEnabled() bool {
	enabled, err := strconv.ParseBool(os.Getenv(rleEnableAllEnvVar))
	return err == nil && enabled
}
