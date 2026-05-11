// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type rootFlagsDefinition struct {
	Debug    bool
	NoPrompt bool
}

// Enable access to the global command flags
var rootFlags rootFlagsDefinition

func NewRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "models <command> [options]",
		Short:         "Extension for managing custom models in Azure AI Foundry. (Preview)",
		SilenceUsage:  true,
		SilenceErrors: true,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
	}

	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	rootCmd.PersistentFlags().BoolVar(
		&rootFlags.Debug,
		"debug",
		false,
		"Enable debug mode",
	)

	rootCmd.PersistentFlags().BoolVar(
		&rootFlags.NoPrompt,
		"no-prompt",
		false,
		"Runs without prompts. Uses existing values; "+
			"fails if any required value or decision cannot be resolved automatically.",
	)

	rootCmd.AddCommand(newVersionCommand())
	rootCmd.AddCommand(newMetadataCommand())
	rootCmd.AddCommand(newInitCommand())
	rootCmd.AddCommand(newCustomCommand())

	// Top-level aliases for custom model commands (preferred over "custom" subgroup)
	for _, cmd := range newTopLevelCustomCommands() {
		rootCmd.AddCommand(cmd)
	}

	return rootCmd
}

// newTopLevelCustomCommands creates top-level create/list/show/delete commands
// that behave identically to their "custom" subgroup counterparts.
// These are the preferred commands; the "custom" subgroup is deprecated.
func newTopLevelCustomCommands() []*cobra.Command {
	flags := &customFlags{}

	cmds := []*cobra.Command{
		newCustomCreateCommand(flags),
		newCustomListCommand(flags),
		newCustomShowCommand(flags),
		newCustomUpdateCommand(flags),
		newCustomDeleteCommand(flags),
	}

	for _, cmd := range cmds {
		cmd.Flags().StringVarP(&flags.subscriptionId, "subscription", "s", "",
			"Azure subscription ID")
		cmd.Flags().StringVar(&flags.projectEndpoint, "project-endpoint", "",
			"Azure AI Foundry project endpoint URL "+
				"(e.g., https://account.services.ai.azure.com/api/projects/project-name)")

		// Resolve project endpoint before running the command
		origPreRunE := cmd.PreRunE
		cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			if err := resolveProjectEndpoint(ctx, flags); err != nil {
				return err
			}
			if origPreRunE != nil {
				return origPreRunE(cmd, args)
			}
			return nil
		}
	}

	return cmds
}
