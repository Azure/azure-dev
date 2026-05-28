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
		Action: &routines.RoutineAction{Type: "invoke_agent_responses_api", AgentName: "my-agent-name"},
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
	assert.Equal(t, "my-agent-name", got.Action.AgentName)
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
  agent_name: yaml-agent-name
`
	path := filepath.Join(t.TempDir(), "routine.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0600))

	got, err := readRoutineManifest(path)
	require.NoError(t, err)
	assert.Equal(t, "yaml-routine", got.Name)
	assert.Equal(t, "timer", got.Triggers["default"].Type)
	require.NotNil(t, got.Action)
	assert.Equal(t, "yaml-agent-name", got.Action.AgentName)
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
		Action:      &routines.RoutineAction{Type: "invoke_agent_responses_api", AgentName: "a"},
	}
	mergeRoutineFromFile(body, file)

	assert.Equal(t, "from-cli", body.Name, "name must not be overwritten by file")
	assert.Equal(t, "from file", body.Description)
	assert.Equal(t, "schedule", body.Triggers["default"].Type)
	require.NotNil(t, body.Action)
	assert.Equal(t, "a", body.Action.AgentName)
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
		Action: &routines.RoutineAction{Type: "invoke_agent_responses_api", AgentName: "cli-agent"},
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
	assert.Equal(t, "cli-agent", body.Action.AgentName, "body action must win")
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
		Action: &routines.RoutineAction{Type: "invoke_agent_responses_api", AgentName: "old-agent"},
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
		Action: &routines.RoutineAction{Type: "invoke_agent_responses_api", AgentName: "old-agent-name"},
	}
}

func TestApplyUpdateFlags_Description(t *testing.T) {
	t.Parallel()
	r := routineWithScheduleAndAgentResp()
	n, err := applyUpdateFlags(r,
		"new desc", "", "", "", "", "", "", "",
		true, false, false, false, false, false, false, false,
	)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, "new desc", r.Description)
}

func TestApplyUpdateFlags_TimeZone(t *testing.T) {
	t.Parallel()
	r := routineWithScheduleAndAgentResp()
	n, err := applyUpdateFlags(r,
		"", "America/New_York", "", "", "", "", "", "",
		false, true, false, false, false, false, false, false,
	)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, "America/New_York", r.Triggers["default"].TimeZone)
}

func TestApplyUpdateFlags_AgentNameClearsEndpointID(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{
		Action:   &routines.RoutineAction{Type: "invoke_agent_responses_api", AgentEndpointID: "old-ep"},
		Triggers: map[string]routines.RoutineTrigger{"default": {Type: "schedule"}},
	}
	n, err := applyUpdateFlags(r,
		"", "", "", "", "new-agent-name", "", "", "",
		false, false, false, false, true, false, false, false,
	)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	require.NotNil(t, r.Action)
	assert.Equal(t, "new-agent-name", r.Action.AgentName)
	assert.Empty(t, r.Action.AgentEndpointID, "setting agent-name should clear agent-endpoint-id")
}

func TestApplyUpdateFlags_AgentEndpointIDClearsID(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{
		Action:   &routines.RoutineAction{Type: "invoke_agent_responses_api", AgentName: "old-agent-name"},
		Triggers: map[string]routines.RoutineTrigger{"default": {Type: "schedule"}},
	}
	n, err := applyUpdateFlags(r,
		"", "", "", "", "", "new-ep", "", "",
		false, false, false, false, false, true, false, false,
	)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	require.NotNil(t, r.Action)
	assert.Equal(t, "new-ep", r.Action.AgentEndpointID)
	assert.Empty(t, r.Action.AgentName, "setting agent-endpoint-id should clear agent-name")
}

func TestApplyUpdateFlags_MutuallyExclusiveAgentFields(t *testing.T) {
	t.Parallel()
	r := routineWithScheduleAndAgentResp()
	_, err := applyUpdateFlags(r,
		"", "", "", "", "new-agent-name", "new-ep", "", "",
		false, false, false, false, true, true, false, false,
	)
	assert.Error(t, err)
}

func TestApplyUpdateFlags_NoChangesReturnsZero(t *testing.T) {
	t.Parallel()
	r := routineWithScheduleAndAgentResp()
	n, err := applyUpdateFlags(r,
		"", "", "", "", "", "", "", "",
		false, false, false, false, false, false, false, false,
	)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

func TestApplyUpdateFlags_AgentNameRejectedForAgentInvoke(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{
		Action:   &routines.RoutineAction{Type: "invoke_agent_invocations_api", AgentEndpointID: "ep"},
		Triggers: map[string]routines.RoutineTrigger{"default": {Type: "timer"}},
	}
	_, err := applyUpdateFlags(r,
		"", "", "", "", "new-agent-name", "", "", "",
		false, false, false, false, true, false, false, false,
	)
	assert.Error(t, err, "--agent-name must be rejected for agent-invoke actions")
}

func TestApplyUpdateFlags_ConvIDRejectedForAgentInvoke(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{
		Action:   &routines.RoutineAction{Type: "invoke_agent_invocations_api", AgentEndpointID: "ep"},
		Triggers: map[string]routines.RoutineTrigger{"default": {Type: "timer"}},
	}
	_, err := applyUpdateFlags(r,
		"", "", "", "", "", "", "conv-1", "",
		false, false, false, false, false, false, true, false,
	)
	assert.Error(t, err, "--conversation-id must be rejected for agent-invoke actions")
}

func TestApplyUpdateFlags_SessIDRejectedForAgentResponse(t *testing.T) {
	t.Parallel()
	r := routineWithScheduleAndAgentResp()
	_, err := applyUpdateFlags(r,
		"", "", "", "", "", "", "", "sess-1",
		false, false, false, false, false, false, false, true,
	)
	assert.Error(t, err, "--session-id must be rejected for agent-response actions")
}

// TestApplyUpdateFlags_AtSucceedsWithRFC3339 documents the happy path for
// issue #8421 Bug 6 in the update verb.
func TestApplyUpdateFlags_AtSucceedsWithRFC3339(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{
		Triggers: map[string]routines.RoutineTrigger{
			"default": {Type: "timer", At: "2026-01-01T00:00:00Z"},
		},
	}
	n, err := applyUpdateFlags(r,
		"", "", "2026-06-26T21:37:26Z", "", "", "", "", "",
		false, false, true, false, false, false, false, false,
	)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, "2026-06-26T21:37:26Z", r.Triggers["default"].At)
}

// TestApplyUpdateFlags_AtRejectsInvalidString covers issue #8421 Bug 6 in
// the update verb. The new RFC 3339 check must run before the value reaches
// the service.
func TestApplyUpdateFlags_AtRejectsInvalidString(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{
		Triggers: map[string]routines.RoutineTrigger{
			"default": {Type: "timer", At: "2026-01-01T00:00:00Z"},
		},
	}
	_, err := applyUpdateFlags(r,
		"", "", "definitely-not-a-date", "", "", "", "", "",
		false, false, true, false, false, false, false, false,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RFC 3339")
	assert.Contains(t, err.Error(), "definitely-not-a-date")
}

// ─── ensureTimerTriggersHaveAt (issue #8421 Bug 4) ────────────────────────────

// TestEnsureTimerTriggersHaveAt_OKWhenAtPresent documents the no-op case.
func TestEnsureTimerTriggersHaveAt_OKWhenAtPresent(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{
		Triggers: map[string]routines.RoutineTrigger{
			"default": {Type: "timer", At: "2026-01-01T00:00:00Z"},
		},
	}
	assert.NoError(t, ensureTimerTriggersHaveAt(r))
}

// TestEnsureTimerTriggersHaveAt_FailsForTimerMissingAt is the core regression
// guard for Bug 4. The Foundry GET response omits `at` for timer triggers, so
// without this check the GET → mutate → PUT round-trip would forward an
// invalid body to the service.
func TestEnsureTimerTriggersHaveAt_FailsForTimerMissingAt(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{
		Triggers: map[string]routines.RoutineTrigger{
			"default": {Type: "timer", TimeZone: "UTC" /* no At */},
		},
	}
	err := ensureTimerTriggersHaveAt(r)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timer")
	assert.Contains(t, err.Error(), "at")
	assert.Contains(t, err.Error(), "default",
		"the offending trigger key should appear in the error message")
}

// TestEnsureTimerTriggersHaveAt_IgnoresScheduleTriggers confirms the guard
// only fires for timer triggers — schedule (cron) triggers do not have `at`.
func TestEnsureTimerTriggersHaveAt_IgnoresScheduleTriggers(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{
		Triggers: map[string]routines.RoutineTrigger{
			"default": {Type: "schedule", CronExpression: "0 8 * * *"},
		},
	}
	assert.NoError(t, ensureTimerTriggersHaveAt(r))
}

// TestEnsureTimerTriggersHaveAt_NoTriggers handles a routine with no triggers
// (e.g. an action-only definition during edits) without false positives.
func TestEnsureTimerTriggersHaveAt_NoTriggers(t *testing.T) {
	t.Parallel()
	assert.NoError(t, ensureTimerTriggersHaveAt(&routines.Routine{}))
}

// TestEnsureTimerTriggersHaveAt_MultipleTimerTriggersAllListed ensures the
// error message names every offending trigger when there is more than one.
func TestEnsureTimerTriggersHaveAt_MultipleTimerTriggersAllListed(t *testing.T) {
	t.Parallel()
	r := &routines.Routine{
		Triggers: map[string]routines.RoutineTrigger{
			"a": {Type: "timer"},
			"b": {Type: "timer"},
		},
	}
	err := ensureTimerTriggersHaveAt(r)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a")
	assert.Contains(t, err.Error(), "b")
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
		Action: &routines.RoutineAction{Type: "invoke_agent_responses_api", AgentName: "orig-agent-name"},
	}
	act := getAction(r)
	require.NotNil(t, act)
	act.AgentName = "changed"
	assert.Equal(t, "orig-agent-name", r.Action.AgentName)
}
