// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// NewRootCommand builds the `azd ai skill` root command and its subcommand
// graph. The cobra root is constructed by [azdext.NewExtensionRootCommand]
// so that azd's global flags (`--debug`, `--no-prompt`, `--cwd`,
// `-e/--environment`, `--output`) are pre-registered.
//
// The cobra `Name` is `skill`. The extension's namespace `ai.skill` maps it
// under the existing `azd ai` group at install time.
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

	// Configure debug logging once on the root command so every subcommand
	// inherits it. The cleanup func is intentionally discarded: log writes
	// are unbuffered and the OS closes the file on exit.
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

	// Register -p / --project-endpoint as a persistent flag so all subcommands
	// inherit it without redeclaring.
	rootCmd.PersistentFlags().StringP("project-endpoint", "p", "",
		"Foundry project endpoint URL (overrides env vars and global config)")

	// Standard extension commands.
	rootCmd.AddCommand(azdext.NewListenCommand(configureExtensionHost))
	rootCmd.AddCommand(newVersionCommand())
	rootCmd.AddCommand(azdext.NewMetadataCommand("1.0", "azure.ai.skills", func() *cobra.Command {
		return rootCmd
	}))

	// Skill subcommands.
	rootCmd.AddCommand(newCreateCommand(extCtx))
	rootCmd.AddCommand(newUpdateCommand(extCtx))
	rootCmd.AddCommand(newShowCommand(extCtx))
	rootCmd.AddCommand(newListCommand(extCtx))
	rootCmd.AddCommand(newDownloadCommand(extCtx))
	rootCmd.AddCommand(newDeleteCommand(extCtx))

	return rootCmd
}

// configureExtensionHost is the `listen` configuration callback. Skills do not
// need to register any lifecycle hooks, so the callback is a no-op.
func configureExtensionHost(host *azdext.ExtensionHost) {
	_ = host
}
