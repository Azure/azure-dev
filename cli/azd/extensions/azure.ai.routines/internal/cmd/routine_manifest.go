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
		return nil, exterrors.Dependency(
			exterrors.CodeFileNotFound,
			fmt.Sprintf("routine manifest file not found: %s", path),
			"verify the path or rerun without --file",
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

// applyUpdateFlags applies named CLI update flags onto an existing routine body.
// It returns the count of fields changed.
func applyUpdateFlags(
	existing *routines.Routine,
	description, timeZone, at, cronExpression, agentName, agentEndpointID, conversationID, sessionID string,
	descChanged, tzChanged, atChanged, cronChanged, agentNameChanged, agentEpChanged, convIDChanged, sessIDChanged bool,
) (int, error) {
	changed := 0

	if descChanged {
		existing.Description = description
		changed++
	}

	// Trigger field updates
	trigger := getTrigger(existing)
	if tzChanged {
		if trigger == nil {
			return 0, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"cannot set --time-zone: routine has no default trigger",
				"add a trigger by recreating the routine, or omit --time-zone",
			)
		}
		trigger.TimeZone = timeZone
		changed++
	}
	if atChanged {
		if trigger == nil {
			return 0, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"cannot set --at: routine has no default trigger",
				"add a trigger by recreating the routine, or omit --at",
			)
		}
		trigger.At = at
		changed++
	}
	if cronChanged {
		if trigger == nil {
			return 0, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"cannot set --cron: routine has no default trigger",
				"add a trigger by recreating the routine, or omit --cron",
			)
		}
		trigger.CronExpression = cronExpression
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
	if agentNameChanged || agentEpChanged {
		if action == nil {
			return 0, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"cannot update agent fields: routine has no action",
				"add an action by recreating the routine, or omit --agent-name / --agent-endpoint-id",
			)
		}
		if agentNameChanged && action.Type == routines.ActionCLIToWire["agent-invoke"] {
			return 0, exterrors.Validation(
				exterrors.CodeConflictingArguments,
				"--agent-name is not applicable to agent-invoke actions",
				"use --agent-endpoint-id for agent-invoke, or recreate the routine with agent-response",
			)
		}
		// agent-name and agent-endpoint-id are mutually exclusive; specifying one clears the other.
		if agentNameChanged && agentEpChanged && agentName != "" && agentEndpointID != "" {
			return 0, exterrors.Validation(
				exterrors.CodeConflictingArguments,
				"--agent-name and --agent-endpoint-id are mutually exclusive",
				"provide either --agent-name or --agent-endpoint-id, not both",
			)
		}
		if agentNameChanged {
			action.AgentName = agentName
			if agentName != "" {
				action.AgentEndpointID = "" // specifying agent-name clears agent-endpoint-id
			}
			changed++
		}
		if agentEpChanged {
			action.AgentEndpointID = agentEndpointID
			if agentEndpointID != "" {
				action.AgentName = "" // specifying agent-endpoint-id clears agent-name
			}
			changed++
		}
	}
	if convIDChanged {
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
		action.ConversationID = conversationID
		changed++
	}
	if sessIDChanged {
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
		action.SessionID = sessionID
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
