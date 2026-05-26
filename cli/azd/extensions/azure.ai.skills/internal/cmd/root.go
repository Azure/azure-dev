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
		Name:  "skill",
		Use:   "skill <command> [options]",
		Short: fmt.Sprintf("Manage Foundry skills from your terminal. %s", color.YellowString("(Preview)")),
		Long: `Manage Foundry skills — reusable behavioral guidelines an agent can attach
at runtime — from your terminal.

Skills carry either inline JSON (description + Markdown instructions) or a
packaged ZIP archive bundling SKILL.md plus any sibling assets. Use this
command group to create, update, show, list, download, and delete skills in
a Foundry project.`,
	})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.CompletionOptions.DisableDefaultCmd = true

	sdkPreRun := rootCmd.PersistentPreRunE
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if sdkPreRun != nil {
			if err := sdkPreRun(cmd, args); err != nil {
				return err
			}
		}
		setupDebugLogging(cmd.Flags())
		return nil
	}

	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})

	rootCmd.PersistentFlags().StringP("project-endpoint", "p", "",
		"Foundry project endpoint URL (overrides env vars and global config)")

	rootCmd.AddCommand(azdext.NewListenCommand(configureExtensionHost))
	rootCmd.AddCommand(newVersionCommand())
	rootCmd.AddCommand(newMetadataCommand(rootCmd))
	rootCmd.AddCommand(newContextCommand())

	rootCmd.AddCommand(newCreateCommand(extCtx))
	rootCmd.AddCommand(newUpdateCommand(extCtx))
	rootCmd.AddCommand(newShowCommand(extCtx))
	rootCmd.AddCommand(newListCommand(extCtx))
	rootCmd.AddCommand(newDownloadCommand(extCtx))
	rootCmd.AddCommand(newDeleteCommand(extCtx))

	return rootCmd
}

// configureExtensionHost is the listen callback. Skills register no
// lifecycle hooks, so it's a no-op.
func configureExtensionHost(host *azdext.ExtensionHost) { _ = host }
