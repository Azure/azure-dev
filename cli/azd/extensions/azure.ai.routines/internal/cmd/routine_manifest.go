// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"azure.ai.routines/internal/exterrors"
	"azure.ai.routines/internal/pkg/routines"

	"gopkg.in/yaml.v3"
)

// readRoutineManifest reads and parses a routine manifest from a YAML or JSON file.
func readRoutineManifest(path string) (*routines.Routine, error) {
	// #nosec G304 - path is provided by the user via --file and is intentional
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, exterrors.Dependency(
				exterrors.CodeFileNotFound,
				fmt.Sprintf("routine manifest file not found: %s", path),
				"verify the path or rerun without --file",
			)
		}
		return nil, exterrors.Dependency(
			exterrors.CodeFileNotFound,
			fmt.Sprintf("unable to read routine manifest file %s: %v", path, err),
			"check file permissions and ensure the path points to a regular file",
		)
	}

	var r routines.Routine
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &r); err != nil {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidRoutineManifest,
				fmt.Sprintf("failed to parse routine manifest %s: %v", path, err),
				"ensure the file is valid YAML and matches the routine schema",
			)
		}
	case ".json", "":
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidRoutineManifest,
				fmt.Sprintf("failed to parse routine manifest %s: %v", path, err),
				"ensure the file is valid JSON and matches the routine schema",
			)
		}
	default:
		return nil, exterrors.Validation(
			exterrors.CodeInvalidRoutineManifest,
			fmt.Sprintf("unsupported manifest file extension %q", ext),
			"use a .yaml, .yml, or .json file",
		)
	}

	return &r, nil
}

// mergeRoutineFromFile copies non-zero fields from file into body only when the
// corresponding body field is unset (create-mode: body wins). The positional
// <name> and any explicit flag overrides take precedence and are applied by
// the caller.
func mergeRoutineFromFile(body *routines.Routine, file *routines.Routine) {
	if file.Description != "" && body.Description == "" {
		body.Description = file.Description
	}
	if file.Enabled != nil && body.Enabled == nil {
		body.Enabled = file.Enabled
	}
	if len(file.Triggers) > 0 && len(body.Triggers) == 0 {
		body.Triggers = file.Triggers
	}
	if file.Action != nil && body.Action == nil {
		body.Action = file.Action
	}
}

// overwriteRoutineFromFile copies non-zero fields from file onto existing,
// overwriting whatever the fetched routine had (update-mode: manifest wins).
// Name is not touched; the caller preserves the positional <name> argument.
// Returns the count of fields overwritten.
func overwriteRoutineFromFile(existing *routines.Routine, file *routines.Routine) int {
	changed := 0
	if file.Description != "" {
		existing.Description = file.Description
		changed++
	}
	if file.Enabled != nil {
		existing.Enabled = file.Enabled
		changed++
	}
	if len(file.Triggers) > 0 {
		existing.Triggers = file.Triggers
		changed++
	}
	if file.Action != nil {
		existing.Action = file.Action
		changed++
	}
	return changed
}

// routineUpdateChanges captures which fields the user wants to update on an
// existing routine and the new values for those fields. Each field is paired
// with a "changed" bool because the empty string is a meaningful "clear this
// field" value when the user explicitly passed the flag.
type routineUpdateChanges struct {
	description string
	timeZone    string
	at          string
	cron        string

	// github_issue
	connectionID string
	owner        string
	repository   string
	issueEvent   string

	// custom
	provider       string
	eventName      string
	parametersJSON string

	// action
	agentName       string
	agentEndpointID string
	conversationID  string
	sessionID       string

	descChanged, tzChanged, atChanged, cronChanged       bool
	connChanged, ownerChanged, repoChanged, eventChanged bool
	providerChanged, eventNameChanged, paramsChanged     bool
	agentNameChanged, agentEpChanged                     bool
	convIDChanged, sessIDChanged                         bool
}

// applyUpdateFlags applies named CLI update flags onto an existing routine body.
// It returns the count of fields changed.
func applyUpdateFlags(existing *routines.Routine, c routineUpdateChanges) (int, error) {
	changed := 0

	if c.descChanged {
		existing.Description = c.description
		changed++
	}

	// Trigger field updates
	trigger := getTrigger(existing)
	triggerType := ""
	if trigger != nil {
		triggerType = trigger.Type
	}
	mustHaveTrigger := func(flagName string) error {
		if trigger == nil {
			return exterrors.Validation(
				exterrors.CodeInvalidParameter,
				fmt.Sprintf("cannot set %s: routine has no default trigger", flagName),
				fmt.Sprintf("add a trigger by recreating the routine, or omit %s", flagName),
			)
		}
		return nil
	}
	wrongTrigger := func(flagName, wantType string) error {
		return exterrors.Validation(
			exterrors.CodeConflictingArguments,
			fmt.Sprintf("%s is not applicable to trigger type %q", flagName, triggerType),
			fmt.Sprintf("use %s only when the routine's trigger is %s", flagName, wantType),
		)
	}

	if c.tzChanged {
		if err := mustHaveTrigger("--time-zone"); err != nil {
			return 0, err
		}
		if triggerType != "schedule" {
			return 0, wrongTrigger("--time-zone", "recurring (schedule)")
		}
		trigger.TimeZone = c.timeZone
		changed++
	}
	if c.atChanged {
		if err := mustHaveTrigger("--at"); err != nil {
			return 0, err
		}
		if triggerType != "timer" {
			return 0, wrongTrigger("--at", "timer")
		}
		trigger.At = c.at
		changed++
	}
	if c.cronChanged {
		if err := mustHaveTrigger("--cron"); err != nil {
			return 0, err
		}
		if triggerType != "schedule" {
			return 0, wrongTrigger("--cron", "recurring (schedule)")
		}
		trigger.CronExpression = c.cron
		changed++
	}
	if c.connChanged {
		if err := mustHaveTrigger("--connection-id"); err != nil {
			return 0, err
		}
		if triggerType != "github_issue" {
			return 0, wrongTrigger("--connection-id", "github-issue")
		}
		trigger.ConnectionID = c.connectionID
		changed++
	}
	if c.ownerChanged {
		if err := mustHaveTrigger("--owner"); err != nil {
			return 0, err
		}
		if triggerType != "github_issue" {
			return 0, wrongTrigger("--owner", "github-issue")
		}
		trigger.Owner = c.owner
		changed++
	}
	if c.repoChanged {
		if err := mustHaveTrigger("--repository"); err != nil {
			return 0, err
		}
		if triggerType != "github_issue" {
			return 0, wrongTrigger("--repository", "github-issue")
		}
		trigger.Repository = c.repository
		changed++
	}
	if c.eventChanged {
		if err := mustHaveTrigger("--issue-event"); err != nil {
			return 0, err
		}
		if triggerType != "github_issue" {
			return 0, wrongTrigger("--issue-event", "github-issue")
		}
		switch c.issueEvent {
		case routines.GitHubIssueEventOpened, routines.GitHubIssueEventClosed:
		default:
			return 0, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				fmt.Sprintf("unsupported --issue-event value %q", c.issueEvent),
				"supported values: opened, closed",
			)
		}
		trigger.IssueEvent = c.issueEvent
		changed++
	}
	if c.providerChanged {
		if err := mustHaveTrigger("--provider"); err != nil {
			return 0, err
		}
		if triggerType != "custom" {
			return 0, wrongTrigger("--provider", "custom")
		}
		trigger.Provider = c.provider
		changed++
	}
	if c.eventNameChanged {
		if err := mustHaveTrigger("--event-name"); err != nil {
			return 0, err
		}
		if triggerType != "custom" {
			return 0, wrongTrigger("--event-name", "custom")
		}
		trigger.EventName = c.eventName
		changed++
	}
	if c.paramsChanged {
		if err := mustHaveTrigger("--parameters"); err != nil {
			return 0, err
		}
		if triggerType != "custom" {
			return 0, wrongTrigger("--parameters", "custom")
		}
		if c.parametersJSON == "" {
			trigger.Parameters = nil
		} else {
			var params map[string]any
			if err := json.Unmarshal([]byte(c.parametersJSON), &params); err != nil {
				return 0, exterrors.Validation(
					exterrors.CodeInvalidParameter,
					fmt.Sprintf("--parameters is not valid JSON: %v", err),
					"provide a JSON object literal, e.g. --parameters '{\"key\":\"value\"}'",
				)
			}
			trigger.Parameters = &params
		}
		changed++
	}
	if trigger != nil {
		if existing.Triggers == nil {
			existing.Triggers = make(map[string]routines.RoutineTrigger)
		}
		existing.Triggers[routines.DefaultTriggerKey] = *trigger
	}

	// Action field updates
	action := getAction(existing)
	if c.agentNameChanged || c.agentEpChanged {
		if action == nil {
			return 0, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"cannot update agent fields: routine has no action",
				"add an action by recreating the routine, or omit --agent-name / --agent-endpoint-id",
			)
		}
		// agent-name and agent-endpoint-id are mutually exclusive; specifying one clears the other.
		if c.agentNameChanged && c.agentEpChanged && c.agentName != "" && c.agentEndpointID != "" {
			return 0, exterrors.Validation(
				exterrors.CodeConflictingArguments,
				"--agent-name and --agent-endpoint-id are mutually exclusive",
				"provide either --agent-name or --agent-endpoint-id, not both",
			)
		}
		if c.agentNameChanged {
			action.AgentName = c.agentName
			if c.agentName != "" {
				action.AgentEndpointID = "" // specifying agent-name clears agent-endpoint-id
			}
			changed++
		}
		if c.agentEpChanged {
			action.AgentEndpointID = c.agentEndpointID
			if c.agentEndpointID != "" {
				action.AgentName = "" // specifying agent-endpoint-id clears agent-name
			}
			changed++
		}
	}
	if c.convIDChanged {
		if action == nil {
			return 0, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"cannot set --conversation-id: routine has no action",
				"add an action by recreating the routine, or omit --conversation-id",
			)
		}
		if action.Type == routines.ActionCLIToWire["agent-invoke"] {
			return 0, exterrors.Validation(
				exterrors.CodeConflictingArguments,
				"--conversation-id is not applicable to agent-invoke actions",
				"use --session-id for agent-invoke, or recreate the routine with agent-response",
			)
		}
		action.Conversation = c.conversationID
		changed++
	}
	if c.sessIDChanged {
		if action == nil {
			return 0, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"cannot set --session-id: routine has no action",
				"add an action by recreating the routine, or omit --session-id",
			)
		}
		if action.Type == routines.ActionCLIToWire["agent-response"] {
			return 0, exterrors.Validation(
				exterrors.CodeConflictingArguments,
				"--session-id is not applicable to agent-response actions",
				"use --conversation-id for agent-response, or recreate the routine with agent-invoke",
			)
		}
		action.SessionID = c.sessionID
		changed++
	}
	if action != nil {
		existing.Action = action
	}

	return changed, nil
}

// getTrigger returns a copy of the default trigger, or nil.
func getTrigger(r *routines.Routine) *routines.RoutineTrigger {
	if t, ok := r.Triggers[routines.DefaultTriggerKey]; ok {
		cp := t
		return &cp
	}
	return nil
}

// getAction returns a copy of the routine action, or nil.
func getAction(r *routines.Routine) *routines.RoutineAction {
	if r.Action == nil {
		return nil
	}
	cp := *r.Action
	return &cp
}
