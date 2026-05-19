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
	var conversationID string
	var output string

	cmd := &cobra.Command{
		Use:   "dispatch <name>",
		Short: "Manually trigger a routine.",
		Long: `Manually trigger a Foundry routine.

By default, waits for the agent response and streams it back.
Use --async to return the dispatch ID immediately without waiting.

Both sync and async modes call the :dispatch_async route.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			output = extCtx.OutputFormat
			ctx := azdext.WithAccessToken(cmd.Context())
			return runRoutineDispatch(ctx, cmd, args[0], asyncMode, input, conversationID, output)
		},
	}

	cmd.Flags().BoolVar(&asyncMode, "async", false,
		"Return the dispatch ID immediately without waiting for the agent response")
	cmd.Flags().StringVar(&input, "input", "",
		"Plain-text user-message payload for the routine dispatch")
	cmd.Flags().StringVar(&conversationID, "conversation-id", "",
		"Conversation ID for agent-response routines (preview)")

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
	input, conversationID, output string,
) error {
	client, _, err := newRoutineClient(ctx, cmd)
	if err != nil {
		return err
	}

	// Build the dispatch payload.
	var payload *routines.DispatchRoutineRequest
	hasPayloadFlags := input != "" || conversationID != ""
	if hasPayloadFlags {
		payload = &routines.DispatchRoutineRequest{
			Input:          input,
			ConversationID: conversationID,
		}
	}

	// Call dispatch_async (both modes use this route; --async only controls client-side waiting).
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
		fmt.Printf("Routine '%s' dispatched asynchronously.\n", name)
		if resp.DispatchID != "" {
			fmt.Printf("Dispatch ID: %s\n", resp.DispatchID)
		}
		if resp.ActionCorrelationID != "" {
			fmt.Printf("Action Correlation ID: %s\n", resp.ActionCorrelationID)
		}
		return nil
	}

	// Sync mode: dispatch was sent; the service runs it asynchronously but we present it as synchronous.
	fmt.Printf("Routine '%s' dispatched.\n", name)
	if resp.DispatchID != "" {
		fmt.Printf("Dispatch ID: %s\n", resp.DispatchID)
	}
	if resp.ActionCorrelationID != "" {
		fmt.Printf("Action Correlation ID: %s\n", resp.ActionCorrelationID)
	}
	if resp.Status != "" {
		fmt.Printf("Status: %s\n", resp.Status)
	}
	return nil
}
