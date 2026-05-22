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
	t.Parallel()
	r := &routines.Routine{
		Name:        "test-routine",
		Description: "a test routine",
		Triggers: map[string]routines.RoutineTrigger{
			"default": {Type: "schedule", CronExpression: "0 8 * * 1-5"},
		},
		Action: &routines.RoutineAction{Type: "invoke_agent_responses_api", AgentID: "my-agent-id"},
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
	assert.Equal(t, "0 8 * * 1-5", got.Triggers["default"].CronExpression)
	require.NotNil(t, got.Action)
	assert.Equal(t, "my-agent-id", got.Action.AgentID)
}

func TestReadRoutineManifest_YAML(t *testing.T) {
	t.Parallel()
	yaml := `name: yaml-routine
description: yaml desc
triggers:
  default:
    type: timer
    at: "2026-01-01T00:00:00Z"
action:
  type: invoke_agent_responses_api
  agent_id: yaml-agent-id
`
	path := filepath.Join(t.TempDir(), "routine.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0600))

	got, err := readRoutineManifest(path)
	require.NoError(t, err)
	assert.Equal(t, "yaml-routine", got.Name)
	assert.Equal(t, "timer", got.Triggers["default"].Type)
	require.NotNil(t, got.Action)
	assert.Equal(t, "yaml-agent-id", got.Action.AgentID)
}

func TestReadRoutineManifest_FileNotFound(t *testing.T) {
	t.Parallel()
	_, err := readRoutineManifest("/nonexistent/path/routine.yaml")
	assert.Error(t, err)
}

func TestReadRoutineManifest_UnsupportedExtension(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "routine.toml")
	require.NoError(t, os.WriteFile(path, []byte("name = 'x'"), 0600))

	_, err := readRoutineManifest(path)
	assert.Error(t, err)
}

// ─── mergeRoutineFromFile ─────────────────────────────────────────────────────

func TestMergeRoutineFromFile_FileFieldsMergedWhenBodyEmpty(t *testing.T) {
	t.Parallel()
	body := &routines.Routine{Name: "from-cli"}
	file := &routines.Routine{
		Description: "from file",
		Triggers:    map[string]routines.RoutineTrigger{"default": {Type: "schedule", CronExpression: "* * * * *"}},
		Action:      &routines.RoutineAction{Type: "invoke_agent_responses_api", AgentID: "a"},
	}
	mergeRoutineFromFile(body, file)

	assert.Equal(t, "from-cli", body.Name, "name must not be overwritten by file")
	assert.Equal(t, "from file", body.Description)
	assert.Equal(t, "schedule", body.Triggers["default"].Type)
	require.NotNil(t, body.Action)
	assert.Equal(t, "a", body.Action.AgentID)
}

func TestMergeRoutineFromFile_BodyFieldsWinOverFile(t *testing.T) {
	t.Parallel()
	body := &routines.Routine{
		Name:        "from-cli",
		Description: "cli description",
		Enabled:     new(true),
		Triggers: map[string]routines.RoutineTrigger{
			"default": {Type: "timer", At: "2026-01-01T00:00:00Z"},
		},
		Action: &routines.RoutineAction{Type: "invoke_agent_responses_api", AgentID: "cli-agent"},
	}
	file := &routines.Routine{
		Description: "file description",
		Triggers: map[string]routines.RoutineTrigger{
			"default": {Type: "schedule", CronExpression: "* * * * *"},
		},
		Action: &routines.RoutineAction{Type: "invoke_agent_invocations_api", AgentEndpointID: "ep"},
	}
	mergeRoutineFromFile(body, file)

	assert.Equal(t, "cli description", body.Description, "body description must win")
	assert.Equal(t, "timer", body.Triggers["default"].Type, "body trigger must win")
	require.NotNil(t, body.Action)
	assert.Equal(t, "cli-agent", body.Action.AgentID, "body action must win")
}

// ─── overwriteRoutineFromFile ──────────────────────────────────────────────────

func TestOverwriteRoutineFromFile_ManifestWins(t *testing.T) {
	t.Parallel()
	existing := &routines.Routine{
		Name:        "fetched-name",
		Description: "old description",
		Enabled:     new(true),
		Triggers: map[string]routines.RoutineTrigger{
			"default": {Type: "timer", At: "2026-01-01T00:00:00Z"},
		},
		Action: &routines.RoutineAction{Type: "invoke_agent_responses_api", AgentID: "old-agent"},
	}
	file := &routines.Routine{
		Description: "new description from manifest",
		Enabled:     new(false),
		Triggers: map[string]routines.RoutineTrigger{
			"default": {Type: "schedule", CronExpression: "0 9 * * *"},
		},
		Action: &routines.RoutineAction{Type: "invoke_agent_invocations_api", AgentEndpointID: "new-ep"},
	}
	n := overwriteRoutineFromFile(existing, file)

	assert.Equal(t, 4, n, "all four fields should be counted as changed")
	assert.Equal(t, "fetched-name", existing.Name, "name must not be overwritten by file")
	assert.Equal(t, "new description from manifest", existing.Description)
	require.NotNil(t, existing.Enabled)
	assert.False(t, *existing.Enabled)
	assert.Equal(t, "schedule", existing.Triggers["default"].Type)
	require.NotNil(t, existing.Action)
	assert.Equal(t, "invoke_agent_invocations_api", existing.Action.Type)
}

func TestOverwriteRoutineFromFile_EmptyManifestChangesNothing(t *testing.T) {
	t.Parallel()
	existing := &routines.Routine{
		Name:        "my-routine",
		Description: "keep this",
	}
	n := overwriteRoutineFromFile(existing, &routines.Routine{})
	assert.Equal(t, 0, n)
	assert.Equal(t, "keep this", existing.Description)
}



func routineWithScheduleAndAgentResp() *routines.Routine {
	return &routines.Routine{
		Name:        "my-routine",
		Description: "old desc",
		Triggers: map[string]routines.RoutineTrigger{
			"default": {Type: "schedule", CronExpression: "0 8 * * *", TimeZone: "UTC"},
		},
		Action: &routines.RoutineAction{Type: "invoke_agent_responses_api", AgentID: "old-agent-id"},
	}
}

func TestApplyUpdateFlags_Description(t *testing.T) {
	t.Parallel()
	r := routineWithScheduleAndAgentResp()
	n, err := applyUpdateFlags(r,
		"new desc", "", "", "", "", "", "",
		true, false, false, false, false, false, false,
	)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, "new desc", r.Description)
}

func TestApplyUpdateFlags_TimeZone(t *testing.T) {
	t.Parallel()
	r := routineWithScheduleAndAgentResp()
	n, err := applyUpdateFlags(r,
		"", "America/New_York", "", "", "", "", "",
		false, true, false, false, false, false, false,
	)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, "America/New_York", r.Triggers["default"].TimeZone)
}

func TestApplyUpdateFlags_AgentIDClearsEndpointID(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{
		Action:   &routines.RoutineAction{Type: "invoke_agent_responses_api", AgentEndpointID: "old-ep"},
		Triggers: map[string]routines.RoutineTrigger{"default": {Type: "schedule"}},
	}
	n, err := applyUpdateFlags(r,
		"", "", "", "new-agent-id", "", "", "",
		false, false, false, true, false, false, false,
	)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	require.NotNil(t, r.Action)
	assert.Equal(t, "new-agent-id", r.Action.AgentID)
	assert.Empty(t, r.Action.AgentEndpointID, "setting agent-id should clear agent-endpoint-id")
}

func TestApplyUpdateFlags_AgentEndpointIDClearsID(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{
		Action:   &routines.RoutineAction{Type: "invoke_agent_responses_api", AgentID: "old-agent-id"},
		Triggers: map[string]routines.RoutineTrigger{"default": {Type: "schedule"}},
	}
	n, err := applyUpdateFlags(r,
		"", "", "", "", "new-ep", "", "",
		false, false, false, false, true, false, false,
	)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	require.NotNil(t, r.Action)
	assert.Equal(t, "new-ep", r.Action.AgentEndpointID)
	assert.Empty(t, r.Action.AgentID, "setting agent-endpoint-id should clear agent-id")
}

func TestApplyUpdateFlags_MutuallyExclusiveAgentFields(t *testing.T) {
	t.Parallel()
	r := routineWithScheduleAndAgentResp()
	_, err := applyUpdateFlags(r,
		"", "", "", "new-agent-id", "new-ep", "", "",
		false, false, false, true, true, false, false,
	)
	assert.Error(t, err)
}

func TestApplyUpdateFlags_NoChangesReturnsZero(t *testing.T) {
	t.Parallel()
	r := routineWithScheduleAndAgentResp()
	n, err := applyUpdateFlags(r,
		"", "", "", "", "", "", "",
		false, false, false, false, false, false, false,
	)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

func TestApplyUpdateFlags_AgentIDRejectedForAgentInvoke(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{
		Action:   &routines.RoutineAction{Type: "invoke_agent_invocations_api", AgentEndpointID: "ep"},
		Triggers: map[string]routines.RoutineTrigger{"default": {Type: "timer"}},
	}
	_, err := applyUpdateFlags(r,
		"", "", "", "new-agent-id", "", "", "",
		false, false, false, true, false, false, false,
	)
	assert.Error(t, err, "--agent-id must be rejected for agent-invoke actions")
}

func TestApplyUpdateFlags_ConvIDRejectedForAgentInvoke(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{
		Action:   &routines.RoutineAction{Type: "invoke_agent_invocations_api", AgentEndpointID: "ep"},
		Triggers: map[string]routines.RoutineTrigger{"default": {Type: "timer"}},
	}
	_, err := applyUpdateFlags(r,
		"", "", "", "", "", "conv-1", "",
		false, false, false, false, false, true, false,
	)
	assert.Error(t, err, "--conversation-id must be rejected for agent-invoke actions")
}

func TestApplyUpdateFlags_SessIDRejectedForAgentResponse(t *testing.T) {
	t.Parallel()
	r := routineWithScheduleAndAgentResp()
	_, err := applyUpdateFlags(r,
		"", "", "", "", "", "", "sess-1",
		false, false, false, false, false, false, true,
	)
	assert.Error(t, err, "--session-id must be rejected for agent-response actions")
}

// ─── getTrigger / getAction ───────────────────────────────────────────────────

func TestGetTrigger_NilWhenEmpty(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{}
	assert.Nil(t, getTrigger(r))
}

func TestGetTrigger_ReturnsCopy(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{
		Triggers: map[string]routines.RoutineTrigger{
			"default": {Type: "schedule", CronExpression: "0 9 * * *"},
		},
	}
	trig := getTrigger(r)
	require.NotNil(t, trig)
	assert.Equal(t, "schedule", trig.Type)
	trig.CronExpression = "changed"
	assert.Equal(t, "0 9 * * *", r.Triggers["default"].CronExpression)
}

func TestGetAction_NilWhenEmpty(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{}
	assert.Nil(t, getAction(r))
}

func TestGetAction_ReturnsCopy(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{
		Action: &routines.RoutineAction{Type: "invoke_agent_responses_api", AgentID: "orig-agent-id"},
	}
	act := getAction(r)
	require.NotNil(t, act)
	act.AgentID = "changed"
	assert.Equal(t, "orig-agent-id", r.Action.AgentID)
}
