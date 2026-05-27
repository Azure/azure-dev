// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"azure.ai.routines/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newRoutineShowCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	var output string

	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show details of a routine.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			output = extCtx.OutputFormat
			ctx := azdext.WithAccessToken(cmd.Context())
			return runRoutineShow(ctx, cmd, args[0], output)
		},
	}

	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"json", "table"}, Default: "table",
	})

	return cmd
}

func runRoutineShow(ctx context.Context, cmd *cobra.Command, name, output string) error {
	client, _, err := newRoutineClient(ctx, cmd)
	if err != nil {
		return err
	}

	routine, err := client.GetRoutine(ctx, name)
	if err != nil {
		if exterrors.IsNotFound(err) {
			return exterrors.ServiceFromStatus(404, exterrors.OpGetRoutine,
				fmt.Sprintf("routine %q not found. Verify the name with 'routine list'.", name))
		}
		return exterrors.ServiceFromAzure(err, exterrors.OpGetRoutine)
	}

	if output == "json" {
		return printJSON(routine)
	}

	routineSummaryTable(routine)
	return nil
}
