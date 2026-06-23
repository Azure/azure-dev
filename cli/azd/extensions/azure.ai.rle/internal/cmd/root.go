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
		Name:  "rle",
		Use:   "rle <command> [options]",
		Short: fmt.Sprintf("Manage RLE resources from your terminal. %s", color.YellowString("(Preview)")),
	})

	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.CompletionOptions.DisableDefaultCmd = true
	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})

	rootCmd.AddCommand(newCreateCommand())
	rootCmd.AddCommand(newListCommand())
	rootCmd.AddCommand(newModifyCommand())
	rootCmd.AddCommand(newSandboxCommand())
	rootCmd.AddCommand(newShowCommand())
	rootCmd.AddCommand(newVersionsCommand())
	rootCmd.AddCommand(newVersionCommand(&extCtx.OutputFormat))
	rootCmd.AddCommand(newMetadataCommand(rootCmd))

	return rootCmd
}
