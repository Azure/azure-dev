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

// routineCreateFlags holds validated input for the create command.
type routineCreateFlags struct {
	name            string
	trigger         string
	cron            string
	timeZone        string
	at              string
	action          string
	agentID         string
	agentEndpointID string
	conversationID  string
	sessionID       string
	description     string
	enabled         bool
	force           bool
	file            string
	output          string
}

func newRoutineCreateCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &routineCreateFlags{
		enabled: true, // default to enabled on creation
	}

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new routine.",
		Long: `Create a new Foundry routine.

A routine pairs a trigger (--trigger) with an action (--action).
Use --file to create from a YAML/JSON manifest file instead of individual flags.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.name = args[0]
			flags.output = extCtx.OutputFormat
			ctx := azdext.WithAccessToken(cmd.Context())
			return runRoutineCreate(ctx, cmd, flags)
		},
	}

	cmd.Flags().StringVar(&flags.trigger, "trigger", "",
		"Trigger type: recurring, timer (required unless --file is used)")
	cmd.Flags().StringVar(&flags.cron, "cron", "",
		"Cron expression for recurring trigger (e.g. '0 8 * * 1-5')")
	cmd.Flags().StringVar(&flags.timeZone, "time-zone", "UTC",
		"Time zone for the trigger (e.g. 'America/New_York')")
	cmd.Flags().StringVar(&flags.at, "at", "",
		"ISO 8601 datetime for timer trigger (e.g. '2026-04-24T15:00:00Z')")
	cmd.Flags().StringVar(&flags.action, "action", "agent-response",
		"Action type: agent-response (default), agent-invoke")
	cmd.Flags().StringVar(&flags.agentID, "agent-id", "",
		"Project-scoped agent ID (for agent-response action)")
	cmd.Flags().StringVar(&flags.agentEndpointID, "agent-endpoint-id", "",
		"Agent endpoint ID (for agent-response or agent-invoke action)")
	cmd.Flags().StringVar(&flags.conversationID, "conversation-id", "",
		"Conversation ID (for agent-response action, preview)")
	cmd.Flags().StringVar(&flags.sessionID, "session-id", "",
		"Session ID (for agent-invoke action)")
	cmd.Flags().StringVar(&flags.description, "description", "",
		"Description for the routine")
	cmd.Flags().BoolVar(&flags.enabled, "enabled", true,
		"Whether the routine is enabled on creation")
	cmd.Flags().BoolVar(&flags.force, "force", false,
		"Overwrite an existing routine with the same name (upsert)")
	cmd.Flags().StringVar(&flags.file, "file", "",
		"Path to a YAML or JSON routine manifest file")

	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"json", "table"}, Default: "table",
	})

	return cmd
}

func runRoutineCreate(ctx context.Context, cmd *cobra.Command, flags *routineCreateFlags) error {
	// --file and --trigger are mutually exclusive
	if flags.file != "" && flags.trigger != "" {
		return exterrors.Validation(
			exterrors.CodeConflictingArguments,
			"--file and --trigger are mutually exclusive",
			"provide either --file or --trigger, not both",
		)
	}

	var body routines.Routine
	body.Name = flags.name
	body.Enabled = new(flags.enabled)
	if flags.description != "" {
		body.Description = flags.description
	}

	if flags.file != "" {
		// File-based creation: read and parse the manifest.
		r, err := readRoutineManifest(flags.file)
		if err != nil {
			return err
		}
		// Merge: CLI flags override file fields.
		mergeRoutineFromFile(&body, r)
		if flags.description != "" {
			body.Description = flags.description
		}
		// name always comes from the positional arg.
		body.Name = flags.name
	} else {
		// Flag-based creation: build trigger + action from flags.
		if flags.trigger == "" {
			return exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"--trigger is required when --file is not provided",
				"specify --trigger recurring, --trigger timer, or use --file",
			)
		}

		trigger, err := buildTrigger(flags)
		if err != nil {
			return err
		}
		body.Triggers = map[string]routines.RoutineTrigger{
			routines.DefaultTriggerKey: trigger,
		}

		action, err := buildAction(
			flags.action, flags.agentID, flags.agentEndpointID,
			flags.conversationID, flags.sessionID,
		)
		if err != nil {
			return err
		}
		body.Action = &action
	}

	client, _, err := newRoutineClient(ctx, cmd)
	if err != nil {
		return err
	}

	// Check if exists when --force is not set.
	if !flags.force {
		existing, err := client.GetRoutine(ctx, flags.name)
		if err != nil && !exterrors.IsNotFound(err) {
			return exterrors.ServiceFromAzure(err, exterrors.OpGetRoutine)
		}
		if existing != nil {
			return exterrors.Validation(
				exterrors.CodeRoutineAlreadyExists,
				fmt.Sprintf("routine %q already exists", flags.name),
				"use --force to overwrite the existing routine, or pick a different name",
			)
		}
	}

	result, err := client.PutRoutine(ctx, flags.name, &body)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpCreateRoutine)
	}

	if flags.output == "json" {
		return printJSON(result)
	}

	fmt.Printf("Routine '%s' created.\n\n", result.Name)
	routineSummaryTable(result)
	return nil
}

// buildTrigger constructs a RoutineTrigger from CLI flags.
func buildTrigger(flags *routineCreateFlags) (routines.RoutineTrigger, error) {
	wireType, ok := routines.TriggerCLIToWire[flags.trigger]
	if !ok {
		return routines.RoutineTrigger{}, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("unknown trigger type %q", flags.trigger),
			"supported triggers: recurring, timer",
		)
	}

	t := routines.RoutineTrigger{
		Type:     wireType,
		TimeZone: flags.timeZone,
	}

	switch flags.trigger {
	case "recurring":
		if flags.cron == "" {
			return t, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"--cron is required for trigger type 'recurring'",
				"provide a cron expression, e.g. '0 8 * * 1-5'",
			)
		}
		t.CronExpression = flags.cron
	case "timer":
		if flags.at == "" {
			return t, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"--at is required for trigger type 'timer'",
				"provide an ISO 8601 datetime, e.g. '2026-04-24T15:00:00Z'",
			)
		}
		t.At = flags.at
	}

	return t, nil
}

// buildAction constructs a RoutineAction from CLI flags.
func buildAction(actionType, agentID, agentEndpointID, conversationID, sessionID string) (routines.RoutineAction, error) {
	wireType, ok := routines.ActionCLIToWire[actionType]
	if !ok {
		return routines.RoutineAction{}, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("unknown action type %q", actionType),
			"supported actions: agent-response (default), agent-invoke",
		)
	}

	a := routines.RoutineAction{Type: wireType}

	switch actionType {
	case "agent-response":
		if agentID != "" && agentEndpointID != "" {
			return a, exterrors.Validation(
				exterrors.CodeConflictingArguments,
				"--agent-id and --agent-endpoint-id are mutually exclusive for agent-response action",
				"provide either --agent-id or --agent-endpoint-id, not both",
			)
		}
		if agentID == "" && agentEndpointID == "" {
			return a, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"one of --agent-id or --agent-endpoint-id is required for agent-response action",
				"provide --agent-id <id> or --agent-endpoint-id <id>",
			)
		}
		a.AgentID = agentID
		a.AgentEndpointID = agentEndpointID
		a.ConversationID = conversationID
	case "agent-invoke":
		if agentEndpointID == "" {
			return a, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"--agent-endpoint-id is required for agent-invoke action",
				"provide --agent-endpoint-id <id>",
			)
		}
		a.AgentEndpointID = agentEndpointID
		a.SessionID = sessionID
	}

	return a, nil
}
