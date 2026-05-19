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

// routineUpdateFlags holds validated input for the update command.
type routineUpdateFlags struct {
	name            string
	trigger         string // type-switch guard only
	action          string // type-switch guard only
	description     string
	cron            string
	timeZone        string
	at              string
	agentName       string
	agentEndpointID string
	conversationID  string
	sessionID       string
	file            string
	output          string
}

func newRoutineUpdateCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &routineUpdateFlags{}

	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Update an existing routine.",
		Long: `Update fields on an existing Foundry routine.

Only the named flags change; all other fields are preserved verbatim.
To change the trigger or action type, delete and recreate the routine.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.name = args[0]
			flags.output = extCtx.OutputFormat
			ctx := azdext.WithAccessToken(cmd.Context())
			return runRoutineUpdate(ctx, cmd, flags)
		},
	}

	// Type-switch guards — registered to surface a friendly error, never used for actual update.
	cmd.Flags().StringVar(&flags.trigger, "trigger", "",
		"Not allowed on update: trigger types are immutable. Delete and recreate to change.")
	cmd.Flags().StringVar(&flags.action, "action", "",
		"Not allowed on update: action types are immutable. Delete and recreate to change.")
	_ = cmd.Flags().MarkHidden("trigger")
	_ = cmd.Flags().MarkHidden("action")

	cmd.Flags().StringVar(&flags.description, "description", "", "New description for the routine")
	cmd.Flags().StringVar(&flags.cron, "cron", "", "New cron expression for recurring trigger")
	cmd.Flags().StringVar(&flags.timeZone, "time-zone", "", "New time zone for the trigger")
	cmd.Flags().StringVar(&flags.at, "at", "", "New ISO 8601 datetime for timer trigger")
	cmd.Flags().StringVar(&flags.agentName, "agent-name", "", "New agent name")
	cmd.Flags().StringVar(&flags.agentEndpointID, "agent-endpoint-id", "", "New agent endpoint ID")
	cmd.Flags().StringVar(&flags.conversationID, "conversation-id", "", "New conversation ID (preview)")
	cmd.Flags().StringVar(&flags.sessionID, "session-id", "", "New session ID")
	cmd.Flags().StringVar(&flags.file, "file", "", "Path to a YAML/JSON manifest; merged fields win unless overridden by flags")

	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"json", "table"}, Default: "table",
	})

	return cmd
}

func runRoutineUpdate(ctx context.Context, cmd *cobra.Command, flags *routineUpdateFlags) error {
	// Type-switch guard: --trigger and --action are not allowed on update.
	if flags.trigger != "" {
		return exterrors.Validation(
			exterrors.CodeConflictingArguments,
			"--trigger cannot be changed on an existing routine",
			fmt.Sprintf("trigger types are immutable. Run 'routine delete %s' then recreate.", flags.name),
		)
	}
	if flags.action != "" {
		return exterrors.Validation(
			exterrors.CodeConflictingArguments,
			"--action cannot be changed on an existing routine",
			fmt.Sprintf("action types are immutable. Run 'routine delete %s' then recreate.", flags.name),
		)
	}

	client, _, err := newRoutineClient(ctx, cmd)
	if err != nil {
		return err
	}

	// GET the existing routine.
	existing, err := client.GetRoutine(ctx, flags.name)
	if err != nil {
		if exterrors.IsNotFound(err) {
			return exterrors.ServiceFromStatus(404, exterrors.OpGetRoutine,
				fmt.Sprintf("routine %q not found", flags.name))
		}
		return exterrors.ServiceFromAzure(err, exterrors.OpGetRoutine)
	}

	// If --file is provided, merge the manifest first.
	if flags.file != "" {
		manifest, err := readRoutineManifest(flags.file)
		if err != nil {
			return err
		}
		mergeRoutineFromFile(existing, manifest)
	}

	// Apply named flag changes (flag presence, not just non-empty value).
	descChanged := cmd.Flags().Changed("description")
	cronChanged := cmd.Flags().Changed("cron")
	tzChanged := cmd.Flags().Changed("time-zone")
	atChanged := cmd.Flags().Changed("at")
	agentNameChanged := cmd.Flags().Changed("agent-name")
	agentEpChanged := cmd.Flags().Changed("agent-endpoint-id")
	convIDChanged := cmd.Flags().Changed("conversation-id")
	sessIDChanged := cmd.Flags().Changed("session-id")

	changed, err := applyUpdateFlags(
		existing,
		flags.description, flags.cron, flags.timeZone, flags.at,
		flags.agentName, flags.agentEndpointID, flags.conversationID, flags.sessionID,
		descChanged, cronChanged, tzChanged, atChanged,
		agentNameChanged, agentEpChanged, convIDChanged, sessIDChanged,
	)
	if err != nil {
		return err
	}

	if changed == 0 && flags.file == "" {
		fmt.Printf("No changes specified for routine '%s'.\n", flags.name)
		return nil
	}

	// PUT the updated body.
	result, err := client.PutRoutine(ctx, flags.name, existing)
	if err != nil {
		if exterrors.IsNotFound(err) {
			return exterrors.ServiceFromStatus(404, exterrors.OpUpdateRoutine,
				fmt.Sprintf("routine %q was deleted before the update completed", flags.name))
		}
		return exterrors.ServiceFromAzure(err, exterrors.OpUpdateRoutine)
	}

	if flags.output == "json" {
		return printJSON(result)
	}

	fmt.Printf("Routine '%s' updated (%d field(s) changed).\n\n", result.Name, changed)
	routineSummaryTable(result)
	return nil
}
