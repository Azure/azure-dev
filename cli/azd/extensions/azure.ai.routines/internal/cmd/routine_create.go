// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
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
	timeZone        string
	at              string
	cronExpression  string
	connectionID    string
	owner           string
	repository      string
	issueEvent      string
	provider        string
	eventName       string
	parametersJSON  string
	action          string
	agentName       string
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
		"Trigger type: timer, recurring, github-issue, or custom (required unless --file is used)")
	cmd.Flags().StringVar(&flags.timeZone, "time-zone", "UTC",
		"Time zone for the recurring trigger (e.g. 'America/New_York')")
	cmd.Flags().StringVar(&flags.at, "at", "",
		"ISO 8601 datetime for timer trigger (e.g. '2026-04-24T15:00:00Z')")
	cmd.Flags().StringVar(&flags.cronExpression, "cron", "",
		"5-field cron expression for recurring trigger (minimum interval 5 minutes)")
	cmd.Flags().StringVar(&flags.connectionID, "connection-id", "",
		"Workspace connection ID (for github-issue trigger)")
	cmd.Flags().StringVar(&flags.owner, "owner", "",
		"GitHub owner or organization (for github-issue trigger)")
	cmd.Flags().StringVar(&flags.repository, "repository", "",
		"GitHub repository name (for github-issue trigger)")
	cmd.Flags().StringVar(&flags.issueEvent, "issue-event", "",
		"GitHub issue event: opened or closed (for github-issue trigger)")
	cmd.Flags().StringVar(&flags.provider, "provider", "",
		"External event provider (for custom trigger)")
	cmd.Flags().StringVar(&flags.eventName, "event-name", "",
		"Provider-specific event name (for custom trigger)")
	cmd.Flags().StringVar(&flags.parametersJSON, "parameters", "",
		"Provider-specific trigger parameters as a JSON object (for custom trigger)")
	cmd.Flags().StringVar(&flags.action, "action", "agent-response",
		"Action type: agent-response (default), agent-invoke")
	cmd.Flags().StringVar(&flags.agentName, "agent-name", "",
		"Project-scoped agent name (for agent-response or agent-invoke action)")
	cmd.Flags().StringVar(&flags.agentEndpointID, "agent-endpoint-id", "",
		"Agent endpoint ID (for agent-response or agent-invoke action)")
	cmd.Flags().StringVar(&flags.conversationID, "conversation-id", "",
		"Existing conversation to continue (for agent-response action, preview)")
	cmd.Flags().StringVar(&flags.sessionID, "session-id", "",
		"Existing session to continue (for agent-invoke action)")
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
	// Only set Enabled from the flag when the user explicitly passed it.
	// Otherwise let the manifest fill it in (file mode), and the post-merge
	// fallback below defaults to enabled=true.
	if cmd.Flags().Changed("enabled") {
		body.Enabled = new(flags.enabled)
	}
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
				"specify --trigger timer or --trigger recurring, or use --file",
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
			flags.action, flags.agentName, flags.agentEndpointID,
			flags.conversationID, flags.sessionID,
		)
		if err != nil {
			return err
		}
		body.Action = &action
	}

	// Default Enabled to true when neither the flag nor the manifest provided
	// a value. This matches the documented "enabled by default on creation"
	// behavior while still letting a manifest's explicit `enabled: false` win.
	if body.Enabled == nil {
		body.Enabled = new(true)
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
//
// Supported triggers: timer (one-shot, --at), recurring (cron, --cron),
// github-issue (--connection-id/--owner/--repository/--issue-event), and
// custom (--provider/--event-name/--parameters).
func buildTrigger(flags *routineCreateFlags) (routines.RoutineTrigger, error) {
	wireType, ok := routines.TriggerCLIToWire[flags.trigger]
	if !ok {
		return routines.RoutineTrigger{}, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("unknown trigger type %q", flags.trigger),
			"supported triggers: timer, recurring, github-issue, custom",
		)
	}

	t := routines.RoutineTrigger{Type: wireType}

	switch flags.trigger {
	case "timer":
		if flags.at == "" {
			return t, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"--at is required for trigger type 'timer'",
				"provide an ISO 8601 datetime, e.g. '2026-04-24T15:00:00Z'",
			)
		}
		t.At = flags.at
		// timer no longer carries time_zone in the v1 spec; silently ignore the
		// flag's default ("UTC") and only error if the user passed a non-UTC.
		if flags.timeZone != "" && flags.timeZone != "UTC" {
			return t, exterrors.Validation(
				exterrors.CodeConflictingArguments,
				"--time-zone is not applicable to trigger type 'timer'",
				"omit --time-zone; pass an absolute UTC --at instead",
			)
		}
	case "recurring":
		if flags.cronExpression == "" {
			return t, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"--cron is required for trigger type 'recurring'",
				"provide a 5-field cron expression, e.g. '0 8 * * *' (minimum interval 5 minutes)",
			)
		}
		t.CronExpression = flags.cronExpression
		t.TimeZone = flags.timeZone
	case "github-issue":
		if flags.connectionID == "" || flags.owner == "" ||
			flags.repository == "" || flags.issueEvent == "" {
			return t, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"--connection-id, --owner, --repository, and --issue-event are required for trigger type 'github-issue'",
				"provide all four flags, e.g. --connection-id conn --owner github --repository azure-dev --issue-event opened",
			)
		}
		switch flags.issueEvent {
		case routines.GitHubIssueEventOpened, routines.GitHubIssueEventClosed:
		default:
			return t, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				fmt.Sprintf("unsupported --issue-event value %q", flags.issueEvent),
				"supported values: opened, closed",
			)
		}
		t.ConnectionID = flags.connectionID
		t.Owner = flags.owner
		t.Repository = flags.repository
		t.IssueEvent = flags.issueEvent
	case "custom":
		if flags.provider == "" {
			return t, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"--provider is required for trigger type 'custom'",
				"provide --provider <external-provider-id>",
			)
		}
		t.Provider = flags.provider
		t.EventName = flags.eventName
		if flags.parametersJSON != "" {
			var params map[string]any
			if err := json.Unmarshal([]byte(flags.parametersJSON), &params); err != nil {
				return t, exterrors.Validation(
					exterrors.CodeInvalidParameter,
					fmt.Sprintf("--parameters is not valid JSON: %v", err),
					"provide a JSON object literal, e.g. --parameters '{\"key\":\"value\"}'",
				)
			}
			t.Parameters = params
		}
	}

	return t, nil
}

// buildAction constructs a RoutineAction from CLI flags.
func buildAction(actionType, agentName, agentEndpointID, conversationID, sessionID string) (routines.RoutineAction, error) {
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
		if agentName != "" && agentEndpointID != "" {
			return a, exterrors.Validation(
				exterrors.CodeConflictingArguments,
				"--agent-name and --agent-endpoint-id are mutually exclusive for agent-response action",
				"provide either --agent-name or --agent-endpoint-id, not both",
			)
		}
		if agentName == "" && agentEndpointID == "" {
			return a, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"one of --agent-name or --agent-endpoint-id is required for agent-response action",
				"provide --agent-name <id> or --agent-endpoint-id <id>",
			)
		}
		if sessionID != "" {
			return a, exterrors.Validation(
				exterrors.CodeConflictingArguments,
				"--session-id is not applicable to agent-response action",
				"use --session-id with --action agent-invoke, or omit --session-id",
			)
		}
		a.AgentName = agentName
		a.AgentEndpointID = agentEndpointID
		a.Conversation = conversationID
	case "agent-invoke":
		if agentName != "" && agentEndpointID != "" {
			return a, exterrors.Validation(
				exterrors.CodeConflictingArguments,
				"--agent-name and --agent-endpoint-id are mutually exclusive for agent-invoke action",
				"provide either --agent-name or --agent-endpoint-id, not both",
			)
		}
		if agentName == "" && agentEndpointID == "" {
			return a, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"one of --agent-name or --agent-endpoint-id is required for agent-invoke action",
				"provide --agent-name <id> or --agent-endpoint-id <id>",
			)
		}
		if conversationID != "" {
			return a, exterrors.Validation(
				exterrors.CodeConflictingArguments,
				"--conversation-id is not applicable to agent-invoke action",
				"use --session-id for agent-invoke, or omit --conversation-id",
			)
		}
		a.AgentName = agentName
		a.AgentEndpointID = agentEndpointID
		a.SessionID = sessionID
	}

	return a, nil
}
