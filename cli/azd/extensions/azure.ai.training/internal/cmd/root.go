// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"azure.ai.training/internal/helpformat"

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
	rootCmd.Example = `  # Initialize a project and submit a training job
  azd ai training init
  azd ai training job submit --file job.yaml`
	rootCmd.SilenceErrors = true
	rootCmd.CompletionOptions.DisableDefaultCmd = true
	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})

	rootCmd.AddCommand(newVersionCommand())
	rootCmd.AddCommand(newInitCommand(extCtx))
	rootCmd.AddCommand(newJobCommand(extCtx))
	rootCmd.AddCommand(newMetadataCommand())

	helpformat.Install(rootCmd, "azd ai", trainingHelpFooter)

	return rootCmd
}
