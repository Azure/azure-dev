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

func newRoutineDispatchCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	var asyncMode bool
	var input string
	var output string

	cmd := &cobra.Command{
		Use:   "dispatch <name>",
		Short: "Manually trigger a routine.",
		Long: `Manually trigger a Foundry routine.

The service runs the routine asynchronously. By default, the command prints
the dispatch ID and action correlation ID. Use --async to suppress extra
output for scripting; use 'routine run list <name>' to inspect execution
results.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			output = extCtx.OutputFormat
			ctx := azdext.WithAccessToken(cmd.Context())
			return runRoutineDispatch(ctx, cmd, args[0], asyncMode, input, output)
		},
	}

	cmd.Flags().BoolVar(&asyncMode, "async", false,
		"Suppress descriptive output; useful for scripting")
	cmd.Flags().StringVar(&input, "input", "",
		"Plain-text user-message payload for the routine dispatch")

	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"json", "table"}, Default: "table",
	})

	return cmd
}

func runRoutineDispatch(
	ctx context.Context,
	cmd *cobra.Command,
	name string,
	asyncMode bool,
	input, output string,
) error {
	client, _, err := newRoutineClient(ctx, cmd)
	if err != nil {
		return err
	}

	// Build the dispatch payload. The payload wrapper carries a discriminated
	// inner type that must match the routine's action type, so we fetch the
	// routine first to read its action type. We skip the GET when no override
	// is provided (the service uses the action's default input in that case).
	var payload *routines.DispatchRoutineRequest
	if input != "" {
		routine, getErr := client.GetRoutine(ctx, name)
		if getErr != nil {
			if exterrors.IsNotFound(getErr) {
				return exterrors.ServiceFromStatus(404, exterrors.OpDispatchRoutine,
					fmt.Sprintf("routine %q not found. Verify the name with 'routine list'.", name))
			}
			return exterrors.ServiceFromAzure(getErr, exterrors.OpGetRoutine)
		}
		if routine.Action == nil || routine.Action.Type == "" {
			return exterrors.Validation(
				exterrors.CodeInvalidParameter,
				fmt.Sprintf("routine %q has no action configured; cannot dispatch with --input", name),
				"update the routine to add an action before dispatching",
			)
		}
		payload = &routines.DispatchRoutineRequest{
			Payload: &routines.RoutineDispatchPayload{
				Type:  routine.Action.Type,
				Input: input,
			},
		}
	}

	resp, err := client.DispatchRoutineAsync(ctx, name, payload)
	if err != nil {
		if exterrors.IsNotFound(err) {
			return exterrors.ServiceFromStatus(404, exterrors.OpDispatchRoutine,
				fmt.Sprintf("routine %q not found. Verify the name with 'routine list'.", name))
		}
		return exterrors.ServiceFromAzure(err, exterrors.OpDispatchRoutine)
	}

	if output == "json" {
		return printJSON(resp)
	}

	if asyncMode {
		if resp.DispatchID != "" {
			fmt.Println(resp.DispatchID)
		}
		return nil
	}

	fmt.Printf("Routine '%s' dispatched.\n", name)
	if resp.DispatchID != "" {
		fmt.Printf("Dispatch ID: %s\n", resp.DispatchID)
	}
	if resp.ActionCorrelationID != "" {
		fmt.Printf("Action Correlation ID: %s\n", resp.ActionCorrelationID)
	}
	if resp.TaskID != "" {
		fmt.Printf("Task ID: %s\n", resp.TaskID)
	}
	return nil
}
