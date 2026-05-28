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

// routineDeleter is the minimal client surface used by runRoutineDelete.
// Defining it as an interface lets the delete logic — including the defensive
// existence check that fixes issue #8421 Bug 7 — be unit-tested without a
// real HTTP client or live Foundry endpoint.
type routineDeleter interface {
	GetRoutine(ctx context.Context, name string) (*routines.Routine, error)
	DeleteRoutine(ctx context.Context, name string) error
}

func newRoutineDeleteCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	var force bool
	var output string

	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a routine.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			output = extCtx.OutputFormat
			ctx := azdext.WithAccessToken(cmd.Context())
			return runRoutineDelete(ctx, cmd, args[0], force, extCtx.NoPrompt, output)
		},
	}

	cmd.Flags().BoolVar(&force, "force", false,
		"Skip confirmation prompt (required in --no-prompt mode)")

	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"json", "table"}, Default: "table",
	})

	return cmd
}

func runRoutineDelete(ctx context.Context, cmd *cobra.Command, name string, force bool, noPromptEnv bool, output string) error {
	// Combine env-backed no-prompt (AZD_NO_PROMPT) with the explicit CLI flag.
	flagNoPrompt, _ := cmd.Flags().GetBool("no-prompt")
	noPrompt := noPromptEnv || flagNoPrompt

	// In --no-prompt mode, --force is required.
	if noPrompt && !force {
		return exterrors.Validation(
			exterrors.CodeConflictingArguments,
			"--force is required when --no-prompt is set",
			"add --force to skip confirmation in --no-prompt mode",
		)
	}

	// Interactive confirmation prompt (unless --force).
	if !force {
		azdClient, err := azdext.NewAzdClient()
		if err != nil {
			return fmt.Errorf("failed to create azd client for prompt: %w", err)
		}
		defer azdClient.Close()

		resp, promptErr := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
			Options: &azdext.ConfirmOptions{
				Message:      fmt.Sprintf("Delete routine '%s'?", name),
				DefaultValue: new(bool), // default false
			},
		})
		if promptErr != nil {
			return fmt.Errorf("prompt failed: %w", promptErr)
		}
		if resp.Value == nil || !*resp.Value {
			fmt.Println("Delete cancelled.")
			return nil
		}
	}

	client, _, err := newRoutineClient(ctx, cmd)
	if err != nil {
		return err
	}

	return deleteRoutineWithExistenceCheck(ctx, client, name, output)
}

// deleteRoutineWithExistenceCheck does an explicit GET before DELETE so that
// deleting a routine that does not exist surfaces the same "not found" error
// as `routine show` / `routine dispatch`, instead of silently succeeding.
//
// The Foundry DELETE endpoint is idempotent and returns 2xx for missing
// resources; the issue #8421 (Bug 7) bug filer explicitly accepts either a
// fix or an "did not exist" message — this implementation chooses the
// fix-and-404 approach for consistency with the rest of the verb surface.
//
// There is a small TOCTOU race between the GET and the DELETE: if another
// caller deletes the routine in that window, the DELETE will still succeed
// (the endpoint is idempotent), which is the desired user-facing outcome.
func deleteRoutineWithExistenceCheck(
	ctx context.Context, client routineDeleter, name, output string,
) error {
	if _, err := client.GetRoutine(ctx, name); err != nil {
		if exterrors.IsNotFound(err) {
			return exterrors.ServiceFromStatus(404, exterrors.OpDeleteRoutine,
				fmt.Sprintf("routine %q not found. Verify the name with 'routine list'.", name))
		}
		return exterrors.ServiceFromAzure(err, exterrors.OpGetRoutine)
	}

	if err := client.DeleteRoutine(ctx, name); err != nil {
		if exterrors.IsNotFound(err) {
			return exterrors.ServiceFromStatus(404, exterrors.OpDeleteRoutine,
				fmt.Sprintf("routine %q not found. Verify the name with 'routine list'.", name))
		}
		return exterrors.ServiceFromAzure(err, exterrors.OpDeleteRoutine)
	}

	if output == "json" {
		return printJSON(map[string]any{"deleted": true, "name": name})
	}

	fmt.Printf("Routine '%s' deleted.\n", name)
	return nil
}
