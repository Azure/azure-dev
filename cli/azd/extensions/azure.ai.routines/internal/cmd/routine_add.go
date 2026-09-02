// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"azure.ai.routines/internal/exterrors"
	"azure.ai.routines/internal/pkg/routines"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type routineAddFlags struct {
	name   string
	file   string
	output string
}

func newRoutineAddCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &routineAddFlags{}
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add or update a routine service in azure.yaml.",
		Long: `Add or update a host: azure.ai.routine service in the current project's azure.yaml.

This command only updates azure.yaml. Run azd deploy <name> or azd up to
create or update the routine in Microsoft Foundry.`,
		Example: `  azd ai routine add nightly-summary --file ./routines/nightly-summary.yaml
  azd deploy nightly-summary`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.name = args[0]
			flags.output = strings.ToLower(extCtx.OutputFormat)
			action := &routineAddAction{
				flags:  flags,
				load:   readRoutineManifest,
				upsert: upsertRoutineServiceToProject,
				writer: os.Stdout,
			}
			return action.Run(cmd.Context())
		},
	}

	cmd.Flags().StringVar(&flags.file, "file", "", "Path to a YAML or JSON routine manifest file")
	_ = cmd.MarkFlagRequired("file")
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"json", "table"}, Default: "table",
	})

	return cmd
}

type routineAddAction struct {
	flags  *routineAddFlags
	load   func(string) (*routines.Routine, error)
	upsert func(context.Context, *routines.Routine, string) (*routineServiceUpsertResult, error)
	writer io.Writer
}

func (action *routineAddAction) Run(ctx context.Context) error {
	routine, err := action.load(action.flags.file)
	if err != nil {
		return err
	}
	if len(routine.Triggers) == 0 || routine.Action == nil {
		return exterrors.Validation(
			exterrors.CodeInvalidRoutineManifest,
			"routine manifest must define triggers and action",
			"add at least one trigger and an action to the routine manifest",
		)
	}
	routine.Name = action.flags.name

	result, err := action.upsert(ctx, routine, action.flags.file)
	if err != nil {
		return err
	}

	if action.flags.output == "json" {
		encoder := json.NewEncoder(action.writer)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			return fmt.Errorf("writing JSON output: %w", err)
		}
		return nil
	}

	verb := "Updated"
	if result.Created {
		verb = "Added"
	}
	fmt.Fprintf(action.writer, "%s routine '%s' in azure.yaml.\n", verb, result.Name)
	return nil
}
