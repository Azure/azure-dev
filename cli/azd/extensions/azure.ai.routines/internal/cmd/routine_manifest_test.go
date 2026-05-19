// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"azure.ai.routines/internal/pkg/routines"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── readRoutineManifest ──────────────────────────────────────────────────────

func TestReadRoutineManifest_JSON(t *testing.T) {
	r := &routines.Routine{
		Name:        "test-routine",
		Description: "a test routine",
		Triggers: map[string]routines.RoutineTrigger{
			"default": {Type: "schedule", Cron: "0 8 * * 1-5"},
		},
		Actions: map[string]routines.RoutineAction{
			"default": {Type: "invoke_agent_responses_api", AgentName: "my-agent"},
		},
	}
	data, err := json.Marshal(r)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "routine.json")
	require.NoError(t, os.WriteFile(path, data, 0600))

	got, err := readRoutineManifest(path)
	require.NoError(t, err)
	assert.Equal(t, "test-routine", got.Name)
	assert.Equal(t, "a test routine", got.Description)
	assert.Equal(t, "schedule", got.Triggers["default"].Type)
	assert.Equal(t, "0 8 * * 1-5", got.Triggers["default"].Cron)
	assert.Equal(t, "my-agent", got.Actions["default"].AgentName)
}

func TestReadRoutineManifest_YAML(t *testing.T) {
	yaml := `name: yaml-routine
description: yaml desc
triggers:
  default:
    type: timer
    at: "2026-01-01T00:00:00Z"
actions:
  default:
    type: invoke_agent_responses_api
    agent_name: yaml-agent
`
	path := filepath.Join(t.TempDir(), "routine.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0600))

	got, err := readRoutineManifest(path)
	require.NoError(t, err)
	assert.Equal(t, "yaml-routine", got.Name)
	assert.Equal(t, "timer", got.Triggers["default"].Type)
	assert.Equal(t, "yaml-agent", got.Actions["default"].AgentName)
}

func TestReadRoutineManifest_FileNotFound(t *testing.T) {
	_, err := readRoutineManifest("/nonexistent/path/routine.yaml")
	assert.Error(t, err)
}

func TestReadRoutineManifest_UnsupportedExtension(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routine.toml")
	require.NoError(t, os.WriteFile(path, []byte("name = 'x'"), 0600))

	_, err := readRoutineManifest(path)
	assert.Error(t, err)
}

// ─── mergeRoutineFromFile ─────────────────────────────────────────────────────

func TestMergeRoutineFromFile_FileFieldsMergedWhenBodyEmpty(t *testing.T) {
	body := &routines.Routine{Name: "from-cli"}
	file := &routines.Routine{
		Description: "from file",
		Triggers:    map[string]routines.RoutineTrigger{"default": {Type: "schedule", Cron: "* * * * *"}},
		Actions:     map[string]routines.RoutineAction{"default": {Type: "invoke_agent_responses_api", AgentName: "a"}},
	}
	mergeRoutineFromFile(body, file)

	assert.Equal(t, "from-cli", body.Name, "name must not be overwritten by file")
	assert.Equal(t, "from file", body.Description)
	assert.Equal(t, "schedule", body.Triggers["default"].Type)
	assert.Equal(t, "a", body.Actions["default"].AgentName)
}

func TestMergeRoutineFromFile_BodyFieldsWinOverFile(t *testing.T) {
	enabled := true
	body := &routines.Routine{
		Name:        "from-cli",
		Description: "cli description",
		Enabled:     &enabled,
		Triggers:    map[string]routines.RoutineTrigger{"default": {Type: "timer", At: "2026-01-01T00:00:00Z"}},
		Actions:     map[string]routines.RoutineAction{"default": {Type: "invoke_agent_responses_api", AgentName: "cli-agent"}},
	}
	file := &routines.Routine{
		Description: "file description",
		Triggers:    map[string]routines.RoutineTrigger{"default": {Type: "schedule", Cron: "* * * * *"}},
		Actions:     map[string]routines.RoutineAction{"default": {Type: "invoke_agent_invocations_api", AgentEndpointID: "ep"}},
	}
	mergeRoutineFromFile(body, file)

	assert.Equal(t, "cli description", body.Description, "body description must win")
	assert.Equal(t, "timer", body.Triggers["default"].Type, "body trigger must win")
	assert.Equal(t, "cli-agent", body.Actions["default"].AgentName, "body action must win")
}

// ─── applyUpdateFlags ─────────────────────────────────────────────────────────

func routine_with_schedule_and_agentresp() *routines.Routine {
	return &routines.Routine{
		Name:        "my-routine",
		Description: "old desc",
		Triggers: map[string]routines.RoutineTrigger{
			"default": {Type: "schedule", Cron: "0 8 * * *", TimeZone: "UTC"},
		},
		Actions: map[string]routines.RoutineAction{
			"default": {Type: "invoke_agent_responses_api", AgentName: "old-agent"},
		},
	}
}

func TestApplyUpdateFlags_Description(t *testing.T) {
	r := routine_with_schedule_and_agentresp()
	n, err := applyUpdateFlags(r,
		"new desc", "", "", "", "", "", "", "",
		true, false, false, false, false, false, false, false,
	)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, "new desc", r.Description)
}

func TestApplyUpdateFlags_Cron(t *testing.T) {
	r := routine_with_schedule_and_agentresp()
	n, err := applyUpdateFlags(r,
		"", "0 9 * * 1-5", "", "", "", "", "", "",
		false, true, false, false, false, false, false, false,
	)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, "0 9 * * 1-5", r.Triggers["default"].Cron)
}

func TestApplyUpdateFlags_TimeZone(t *testing.T) {
	r := routine_with_schedule_and_agentresp()
	n, err := applyUpdateFlags(r,
		"", "", "America/New_York", "", "", "", "", "",
		false, false, true, false, false, false, false, false,
	)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, "America/New_York", r.Triggers["default"].TimeZone)
}

func TestApplyUpdateFlags_AgentNameClearsEndpointID(t *testing.T) {
	r := &routines.Routine{
		Actions: map[string]routines.RoutineAction{
			"default": {Type: "invoke_agent_responses_api", AgentEndpointID: "old-ep"},
		},
		Triggers: map[string]routines.RoutineTrigger{"default": {Type: "schedule"}},
	}
	n, err := applyUpdateFlags(r,
		"", "", "", "", "new-agent", "", "", "",
		false, false, false, false, true, false, false, false,
	)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, "new-agent", r.Actions["default"].AgentName)
	assert.Empty(t, r.Actions["default"].AgentEndpointID, "setting agent-name should clear agent-endpoint-id")
}

func TestApplyUpdateFlags_AgentEndpointIDClearsName(t *testing.T) {
	r := &routines.Routine{
		Actions: map[string]routines.RoutineAction{
			"default": {Type: "invoke_agent_responses_api", AgentName: "old-agent"},
		},
		Triggers: map[string]routines.RoutineTrigger{"default": {Type: "schedule"}},
	}
	n, err := applyUpdateFlags(r,
		"", "", "", "", "", "new-ep", "", "",
		false, false, false, false, false, true, false, false,
	)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, "new-ep", r.Actions["default"].AgentEndpointID)
	assert.Empty(t, r.Actions["default"].AgentName, "setting agent-endpoint-id should clear agent-name")
}

func TestApplyUpdateFlags_MutuallyExclusiveAgentFields(t *testing.T) {
	r := routine_with_schedule_and_agentresp()
	_, err := applyUpdateFlags(r,
		"", "", "", "", "new-agent", "new-ep", "", "",
		false, false, false, false, true, true, false, false,
	)
	assert.Error(t, err)
}

func TestApplyUpdateFlags_NoChangesReturnsZero(t *testing.T) {
	r := routine_with_schedule_and_agentresp()
	n, err := applyUpdateFlags(r,
		"", "", "", "", "", "", "", "",
		false, false, false, false, false, false, false, false,
	)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

// ─── getTrigger / getAction ───────────────────────────────────────────────────

func TestGetTrigger_NilWhenEmpty(t *testing.T) {
	r := &routines.Routine{}
	assert.Nil(t, getTrigger(r))
}

func TestGetTrigger_ReturnsCopy(t *testing.T) {
	r := &routines.Routine{
		Triggers: map[string]routines.RoutineTrigger{
			"default": {Type: "schedule", Cron: "0 9 * * *"},
		},
	}
	trig := getTrigger(r)
	require.NotNil(t, trig)
	assert.Equal(t, "schedule", trig.Type)
	// Modifying copy must not affect original.
	trig.Cron = "changed"
	assert.Equal(t, "0 9 * * *", r.Triggers["default"].Cron)
}

func TestGetAction_NilWhenEmpty(t *testing.T) {
	r := &routines.Routine{}
	assert.Nil(t, getAction(r))
}

func TestGetAction_ReturnsCopy(t *testing.T) {
	r := &routines.Routine{
		Actions: map[string]routines.RoutineAction{
			"default": {Type: "invoke_agent_responses_api", AgentName: "orig-agent"},
		},
	}
	act := getAction(r)
	require.NotNil(t, act)
	// Modifying copy must not affect original.
	act.AgentName = "changed"
	assert.Equal(t, "orig-agent", r.Actions["default"].AgentName)
}
