// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"azureaiagent/internal/exterrors"

	"github.com/spf13/cobra"
)

func newEvalCommand() *cobra.Command {
	newMigrationCommand := func(name, replacement string) *cobra.Command {
		suggestion := fmt.Sprintf(
			"Install the evaluations extension with 'azd extension install azure.ai.evaluations', then use '%s'. "+
				"Legacy evaluation configuration and arguments are not automatically converted.",
			replacement,
		)
		migrationError := func(cmd *cobra.Command, _ []string) error {
			return exterrors.Compatibility(
				exterrors.CodeDeprecatedEvalCommand,
				fmt.Sprintf(
					"'azd ai %s' is deprecated and no longer runs the legacy evaluation workflow.", cmd.CommandPath()),
				suggestion,
			)
		}
		cmd := &cobra.Command{
			Use:        name,
			Short:      "Migration guidance for legacy agent evaluation",
			Deprecated: fmt.Sprintf("use '%s' from 'azure.ai.evaluations' instead", replacement),
			Long: "The legacy agent evaluation workflow has been removed. " +
				"This command only provides migration guidance.\n\n" +
				suggestion + "\n\n" +
				"Invocations fail without authentication, service requests, or changes to project or environment files. " +
				"No arguments are forwarded to the new commands.",
			Example: "  azd extension install azure.ai.evaluations\n  " + replacement,
			// Reject before persistent pre-run hooks can initialize the SDK or load project context.
			Args: migrationError,
			RunE: migrationError,
		}
		cmd.SetFlagErrorFunc(func(cmd *cobra.Command, _ error) error {
			return migrationError(cmd, nil)
		})
		return cmd
	}

	cmd := newMigrationCommand("eval", "azd ai eval --help")
	for _, migration := range []struct {
		name        string
		replacement string
	}{
		{"init", "azd ai eval init"},
		{"generate", "azd ai eval generate"},
		{"run", "azd ai eval run start"},
		{"update", "azd deploy"},
		{"list", "azd ai eval --help"},
		{"show", "azd ai eval --help"},
	} {
		cmd.AddCommand(newMigrationCommand(migration.name, migration.replacement))
	}
	return cmd
}
