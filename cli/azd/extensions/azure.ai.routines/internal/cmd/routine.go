// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// newRoutineCommand creates the "routine" subcommand group.
func newRoutineCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "routine <command> [options]",
		Short: "Manage Microsoft Foundry Routines. (Preview)",
		Long: `Manage Microsoft Foundry Routines from your terminal.

A routine pairs one trigger (when) with one action (what) on a Foundry project.
For example: "every weekday at 8 AM UTC, invoke the daily-report agent".`,
	}

	// -p / --project-endpoint is a persistent flag so all subcommands inherit it.
	cmd.PersistentFlags().StringP("project-endpoint", "p", "",
		"Foundry project endpoint URL (overrides env var and config)")

	cmd.AddCommand(newRoutineCreateCommand(extCtx))
	cmd.AddCommand(newRoutineUpdateCommand(extCtx))
	cmd.AddCommand(newRoutineShowCommand(extCtx))
	cmd.AddCommand(newRoutineListCommand(extCtx))
	cmd.AddCommand(newRoutineDeleteCommand(extCtx))
	cmd.AddCommand(newRoutineEnableCommand(extCtx))
	cmd.AddCommand(newRoutineDisableCommand(extCtx))
	cmd.AddCommand(newRoutineDispatchCommand(extCtx))
	cmd.AddCommand(newRoutineRunCommand(extCtx))

	return cmd
}
