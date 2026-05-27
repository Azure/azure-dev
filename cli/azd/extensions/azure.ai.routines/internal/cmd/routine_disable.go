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

func newRoutineDisableCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	var output string

	cmd := &cobra.Command{
		Use:   "disable <name>",
		Short: "Disable a routine.",
		Long: `Disable a Foundry routine.

This operation is idempotent: disabling an already-disabled routine is a no-op success.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			output = extCtx.OutputFormat
			ctx := azdext.WithAccessToken(cmd.Context())
			return runRoutineDisable(ctx, cmd, args[0], output)
		},
	}

	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"json", "table"}, Default: "table",
	})

	return cmd
}

func runRoutineDisable(ctx context.Context, cmd *cobra.Command, name, output string) error {
	client, _, err := newRoutineClient(ctx, cmd)
	if err != nil {
		return err
	}

	result, err := client.DisableRoutine(ctx, name)
	if err != nil {
		if exterrors.IsNotFound(err) {
			return exterrors.ServiceFromStatus(404, exterrors.OpDisableRoutine,
				fmt.Sprintf("routine %q not found. Verify the name with 'routine list'.", name))
		}
		return exterrors.ServiceFromAzure(err, exterrors.OpDisableRoutine)
	}

	if output == "json" {
		return printJSON(result)
	}

	fmt.Printf("Routine '%s' disabled.\n", name)
	return nil
}
