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
	if len(file.Actions) > 0 && len(body.Actions) == 0 {
		body.Actions = file.Actions
	}
}

// applyUpdateFlags applies named CLI update flags onto an existing routine body.
// It returns the count of fields changed.
func applyUpdateFlags(
	existing *routines.Routine,
	description, cron, timeZone, at, agentName, agentEndpointID, conversationID, sessionID string,
	descChanged, cronChanged, tzChanged, atChanged, agentNameChanged, agentEpChanged, convIDChanged, sessIDChanged bool,
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
			return 0, fmt.Errorf("cannot set --cron: routine has no default trigger")
		}
		trigger.Cron = cron
		changed++
	}
	if tzChanged {
		if trigger == nil {
			return 0, fmt.Errorf("cannot set --time-zone: routine has no default trigger")
		}
		trigger.TimeZone = timeZone
		changed++
	}
	if atChanged {
		if trigger == nil {
			return 0, fmt.Errorf("cannot set --at: routine has no default trigger")
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
	if agentNameChanged || agentEpChanged {
		if action == nil {
			return 0, fmt.Errorf("cannot update agent fields: routine has no default action")
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
			return 0, fmt.Errorf("cannot set --conversation-id: routine has no default action")
		}
		action.ConversationID = conversationID
		changed++
	}
	if sessIDChanged {
		if action == nil {
			return 0, fmt.Errorf("cannot set --session-id: routine has no default action")
		}
		action.SessionID = sessionID
		changed++
	}
	if action != nil {
		if existing.Actions == nil {
			existing.Actions = make(map[string]routines.RoutineAction)
		}
		existing.Actions[routines.DefaultActionKey] = *action
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

// getAction returns a copy of the default action, or nil.
func getAction(r *routines.Routine) *routines.RoutineAction {
	if a, ok := r.Actions[routines.DefaultActionKey]; ok {
		cp := a
		return &cp
	}
	return nil
}
