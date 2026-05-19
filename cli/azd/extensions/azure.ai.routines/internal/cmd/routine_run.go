// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"azure.ai.routines/internal/exterrors"
	"azure.ai.routines/internal/pkg/routines"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// newRoutineRunCommand creates the "run" subcommand group.
func newRoutineRunCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <command> [options]",
		Short: "Manage routine run history.",
	}

	cmd.AddCommand(newRoutineRunListCommand(extCtx))

	return cmd
}

func newRoutineRunListCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	var top int
	var filter string
	var output string

	cmd := &cobra.Command{
		Use:   "list <routine>",
		Short: "List runs for a routine.",
		Long: `List execution history for a Foundry routine.

Auto-paginates via page tokens. Use --top to cap the total number of results.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			output = extCtx.OutputFormat
			ctx := azdext.WithAccessToken(cmd.Context())
			return runRoutineRunList(ctx, cmd, args[0], top, filter, output)
		},
	}

	cmd.Flags().IntVar(&top, "top", 0,
		"Maximum total number of runs to return (0 = no cap)")
	cmd.Flags().StringVar(&filter, "filter", "",
		"OData filter expression")

	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"json", "table"}, Default: "table",
	})

	return cmd
}

func runRoutineRunList(ctx context.Context, cmd *cobra.Command, routineName string, top int, filter, output string) error {
	client, _, err := newRoutineClient(ctx, cmd)
	if err != nil {
		return err
	}

	items, err := client.ListRoutineRuns(ctx, routineName, routines.ListRoutineRunsOptions{
		Top:    top,
		Filter: filter,
	})
	if err != nil {
		if exterrors.IsNotFound(err) {
			return exterrors.ServiceFromStatus(404, exterrors.OpListRoutineRuns,
				fmt.Sprintf("routine %q not found. Verify the name with 'routine list'.", routineName))
		}
		return exterrors.ServiceFromAzure(err, exterrors.OpListRoutineRuns)
	}

	if output == "json" {
		return printJSON(map[string]any{
			"value":           items,
			"next_page_token": "",
		})
	}

	if len(items) == 0 {
		fmt.Printf("No runs found for routine '%s'.\n", routineName)
		return nil
	}

	tw := newTabWriter()
	defer tw.Flush()
	fmt.Fprintln(tw, "ID\tSTATUS\tSTARTED\tENDED")
	fmt.Fprintln(tw, "--\t------\t-------\t-----")
	for _, run := range items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			run.ID, run.Status, run.StartedAt, run.EndedAt)
	}
	return nil
}
