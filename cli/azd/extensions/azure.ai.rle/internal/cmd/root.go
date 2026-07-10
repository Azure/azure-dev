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

const rleEnableEnvVar = "AZD_AI_RLE_ENABLE"

func NewRootCommand() *cobra.Command {
	rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name:  "rle",
		Use:   "rle <command> [options]",
		Short: fmt.Sprintf("Manage RLE resources from your terminal. %s", color.YellowString("(Preview)")),
	})

	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.CompletionOptions.DisableDefaultCmd = true
	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})

	defaultHelp := rootCmd.HelpFunc()
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if cmd == rootCmd {
			printBanner(cmd.OutOrStdout())
		}
		defaultHelp(cmd, args)
	})

	userCommands := []*cobra.Command{
		newDeployCommand(),
		newInitCommand(),
		newInvokeCommand(),
		newRunCommand(),
	}
	for _, command := range userCommands {
		command.Hidden = !rleCommandsEnabled()
		rootCmd.AddCommand(command)
	}
	rootCmd.AddCommand(newVersionCommand(&extCtx.OutputFormat))
	rootCmd.AddCommand(newMetadataCommand(rootCmd))

	return rootCmd
}

func rleCommandsEnabled() bool {
	enabled, err := strconv.ParseBool(os.Getenv(rleEnableEnvVar))
	return err == nil && enabled
}
