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
			return runRoutineDelete(ctx, cmd, args[0], force, output)
		},
	}

	cmd.Flags().BoolVar(&force, "force", false,
		"Skip confirmation prompt (required in --no-prompt mode)")

	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"json", "table"}, Default: "table",
	})

	return cmd
}

func runRoutineDelete(ctx context.Context, cmd *cobra.Command, name string, force bool, output string) error {
	noPrompt, _ := cmd.Flags().GetBool("no-prompt")

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
