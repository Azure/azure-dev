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

func newRoutineListCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	var output string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all routines in the Foundry project.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			output = extCtx.OutputFormat
			ctx := azdext.WithAccessToken(cmd.Context())
			return runRoutineList(ctx, cmd, output)
		},
	}

	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"json", "table"}, Default: "table",
	})

	return cmd
}

func runRoutineList(ctx context.Context, cmd *cobra.Command, output string) error {
	client, _, err := newRoutineClient(ctx, cmd)
	if err != nil {
		return err
	}

	items, err := client.ListRoutines(ctx)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpListRoutines)
	}

	if output == "json" {
		return printJSON(map[string]any{
			"value":              items,
			"continuation_token": "",
		})
	}

	if len(items) == 0 {
		fmt.Println("No routines found.")
		return nil
	}

	tw := newTabWriter()
	defer tw.Flush()
	fmt.Fprintln(tw, "NAME\tENABLED\tTRIGGER\tACTION")
	fmt.Fprintln(tw, "----\t-------\t-------\t------")
	for _, r := range items {
		triggerType := ""
		if t, ok := r.Triggers[routines.DefaultTriggerKey]; ok {
			triggerType = t.Type
		}
		actionType := ""
		if a, ok := r.Actions[routines.DefaultActionKey]; ok {
			actionType = a.Type
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			r.Name,
			boolStr(r.Enabled),
			triggerType,
			actionType,
		)
	}
	return nil
}
