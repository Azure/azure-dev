// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"strconv"

	"azure.ai.rle/internal/helpformat"

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
	rootCmd.Example = `  # Initialize and run a local RLE environment (enable commands first)
  azd ai rle init
  azd ai rle run`
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
		newListCommand(&extCtx.OutputFormat),
		newShowCommand(&extCtx.OutputFormat),
		newInitCommand(),
		newInvokeCommand(),
		newPublishCommand(),
		newRunCommand(),
	}
	for _, command := range userCommands {
		command.Hidden = !rleCommandsEnabled()
		rootCmd.AddCommand(command)
	}
	versionCmd := newVersionCommand(&extCtx.OutputFormat)
	versionCmd.Example = `  # Display the installed extension version
  azd ai rle version`
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(newMetadataCommand(rootCmd))

	helpformat.Install(rootCmd, "azd ai", rleHelpFooter)

	return rootCmd
}

func rleCommandsEnabled() bool {
	enabled, err := strconv.ParseBool(os.Getenv(rleEnableEnvVar))
	return err == nil && enabled
}
