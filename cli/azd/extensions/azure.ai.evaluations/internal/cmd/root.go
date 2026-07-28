// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// NewRootCommand builds the `azd ai eval` command tree.
func NewRootCommand() *cobra.Command {
	rootCmd, _ := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
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

	return rootCmd
}
