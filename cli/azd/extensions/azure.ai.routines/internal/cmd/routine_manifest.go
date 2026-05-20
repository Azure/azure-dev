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

// mergeRoutineFromFile copies fields from the manifest into body.
// The caller's positional <name> argument wins over any name in the file.
// Individual flag overrides are applied by the caller after this function returns.
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

// applyUpdateFlags applies named CLI update flags onto an existing routine body.
// It returns the count of fields changed.
func applyUpdateFlags(
	existing *routines.Routine,
	description, cron, timeZone, at, agentID, agentEndpointID, conversationID, sessionID string,
	descChanged, cronChanged, tzChanged, atChanged, agentIDChanged, agentEpChanged, convIDChanged, sessIDChanged bool,
) (int, error) {
	changed := 0

	if descChanged {
		existing.Description = description
		changed++
	}

	// Trigger field updates
	trigger := getTrigger(existing)
	if cronChanged {
		if trigger == nil {
			return 0, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"cannot set --cron: routine has no default trigger",
				"add a trigger by recreating the routine, or omit --cron",
			)
		}
		trigger.CronExpression = cron
		changed++
	}
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
	if trigger != nil {
		if existing.Triggers == nil {
			existing.Triggers = make(map[string]routines.RoutineTrigger)
		}
		existing.Triggers[routines.DefaultTriggerKey] = *trigger
	}

	// Action field updates
	action := getAction(existing)
	if agentIDChanged || agentEpChanged {
		if action == nil {
			return 0, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"cannot update agent fields: routine has no action",
				"add an action by recreating the routine, or omit --agent-id / --agent-endpoint-id",
			)
		}
		// agent-id and agent-endpoint-id are mutually exclusive; specifying one clears the other.
		if agentIDChanged && agentEpChanged && agentID != "" && agentEndpointID != "" {
			return 0, exterrors.Validation(
				exterrors.CodeConflictingArguments,
				"--agent-id and --agent-endpoint-id are mutually exclusive",
				"provide either --agent-id or --agent-endpoint-id, not both",
			)
		}
		if agentIDChanged {
			action.AgentID = agentID
			if agentID != "" {
				action.AgentEndpointID = "" // specifying agent-id clears agent-endpoint-id
			}
			changed++
		}
		if agentEpChanged {
			action.AgentEndpointID = agentEndpointID
			if agentEndpointID != "" {
				action.AgentID = "" // specifying agent-endpoint-id clears agent-id
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
