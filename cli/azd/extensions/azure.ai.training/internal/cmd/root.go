// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name:  "training",
		Use:   "training <command> [options]",
		Short: fmt.Sprintf("Extension for Microsoft Foundry training jobs. %s", color.YellowString("(Preview)")),
	})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.CompletionOptions.DisableDefaultCmd = true
	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})

	rootCmd.AddCommand(newVersionCommand())
	rootCmd.AddCommand(newInitCommand(extCtx))
	rootCmd.AddCommand(newJobCommand(extCtx))
	rootCmd.AddCommand(newMetadataCommand())

	return rootCmd
}
