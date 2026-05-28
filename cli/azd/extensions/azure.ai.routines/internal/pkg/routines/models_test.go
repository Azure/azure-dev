// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package routines

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTriggerCLIToWire_AllEntriesPresent(t *testing.T) {
	t.Parallel()
	expected := map[string]string{
		"recurring":    "schedule",
		"timer":        "timer",
		"github-issue": "github_issue",
	}
	assert.Equal(t, expected, TriggerCLIToWire,
		"TriggerCLIToWire must contain all documented CLI→wire mappings")
}

func TestActionCLIToWire_AllEntriesPresent(t *testing.T) {
	t.Parallel()
	expected := map[string]string{
		"agent-response": "invoke_agent_responses_api",
		"agent-invoke":   "invoke_agent_invocations_api",
	}
	assert.Equal(t, expected, ActionCLIToWire,
		"ActionCLIToWire must contain all documented CLI→wire mappings")
}

func TestDefaultKeys(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "default", DefaultTriggerKey)
}

func TestTriggerCLIToWire_NoUnknownEntries(t *testing.T) {
	t.Parallel()
	// Ensure no extra/typo entries sneak in.
	for k := range TriggerCLIToWire {
		switch k {
		case "recurring", "timer", "github-issue":
			// OK
		default:
			t.Errorf("unexpected key %q in TriggerCLIToWire", k)
		}
	}
}

func TestActionCLIToWire_NoUnknownEntries(t *testing.T) {
	t.Parallel()
	for k := range ActionCLIToWire {
		switch k {
		case "agent-response", "agent-invoke":
			// OK
		default:
			t.Errorf("unexpected key %q in ActionCLIToWire", k)
		}
	}
}

// ─── PagedRoutine envelope (issue #8421 Bug 1) ────────────────────────────────

// TestPagedRoutine_SpecEnvelope_AgentsPagedResult verifies that the client
// correctly decodes the AgentsPagedResult<Routine> envelope introduced by
// azure-rest-api-specs PR #43498 — `{ data, first_id, last_id, has_more }`.
// Before the fix the client used the previous `{ value, nextLink }` shape
// and dropped every item, which is the root cause of issue #8421 Bug 1.
func TestPagedRoutine_SpecEnvelope_AgentsPagedResult(t *testing.T) {
	t.Parallel()
	body := `{
		"data": [
			{"name": "e2e-timer-matrix"},
			{"name": "e2e-timer-matrix-file"}
		],
		"first_id": "e2e-timer-matrix",
		"last_id": "e2e-timer-matrix-file",
		"has_more": true
	}`

	var page PagedRoutine
	require.NoError(t, json.Unmarshal([]byte(body), &page))

	items := page.Items()
	require.Len(t, items, 2)
	assert.Equal(t, "e2e-timer-matrix", items[0].Name)
	assert.Equal(t, "e2e-timer-matrix-file", items[1].Name)
	assert.Equal(t, "e2e-timer-matrix-file", page.NextCursor(),
		"NextCursor must surface last_id when has_more is true")
}

// TestPagedRoutine_SpecEnvelope_HasMoreFalse confirms that a terminal page
// (has_more=false) reports no further cursor even when last_id is populated.
func TestPagedRoutine_SpecEnvelope_HasMoreFalse(t *testing.T) {
	t.Parallel()
	body := `{
		"data": [{"name": "only-routine"}],
		"first_id": "only-routine",
		"last_id": "only-routine",
		"has_more": false
	}`

	var page PagedRoutine
	require.NoError(t, json.Unmarshal([]byte(body), &page))

	assert.Len(t, page.Items(), 1)
	assert.Empty(t, page.NextCursor(),
		"NextCursor must be empty on the last page even with last_id populated")
}

// TestPagedRoutine_LegacyEnvelope ensures that during the spec rollout window
// the client still falls back to the previous `{ value, nextLink }` shape, so
// regions that have not yet shipped #43498 keep working.
func TestPagedRoutine_LegacyEnvelope(t *testing.T) {
	t.Parallel()
	body := `{
		"value": [
			{"name": "legacy-routine"}
		],
		"nextLink": "https://example.com/routines?api-version=v1&pageToken=abc"
	}`

	var page PagedRoutine
	require.NoError(t, json.Unmarshal([]byte(body), &page))

	items := page.Items()
	require.Len(t, items, 1)
	assert.Equal(t, "legacy-routine", items[0].Name)
	assert.Empty(t, page.NextCursor(),
		"NextCursor only applies to the spec-shaped envelope")
	assert.Equal(t, "https://example.com/routines?api-version=v1&pageToken=abc",
		page.NextLinkURL())
}

// TestPagedRoutine_EmptyResponse confirms that a service that returns neither
// `data` nor `value` decodes safely to an empty page.
func TestPagedRoutine_EmptyResponse(t *testing.T) {
	t.Parallel()
	var page PagedRoutine
	require.NoError(t, json.Unmarshal([]byte(`{}`), &page))
	assert.Empty(t, page.Items())
	assert.Empty(t, page.NextCursor())
	assert.Empty(t, page.NextLinkURL())
}

// TestPagedRoutine_PrefersDataOverValue guards against a transitional service
// that returns both envelopes simultaneously. Spec wins.
func TestPagedRoutine_PrefersDataOverValue(t *testing.T) {
	t.Parallel()
	body := `{
		"data": [{"name": "spec"}],
		"value": [{"name": "legacy"}]
	}`

	var page PagedRoutine
	require.NoError(t, json.Unmarshal([]byte(body), &page))

	items := page.Items()
	require.Len(t, items, 1)
	assert.Equal(t, "spec", items[0].Name,
		"Items() must prefer the spec-shaped `data` field over the legacy `value` field")
}

// ─── PagedRoutineRun envelope (issue #8421 Bug 2) ─────────────────────────────

// TestPagedRoutineRun_SpecEnvelope_AgentsPagedResult mirrors the routines fix
// for the run-history endpoint, which shares the same AgentsPagedResult shape.
func TestPagedRoutineRun_SpecEnvelope_AgentsPagedResult(t *testing.T) {
	t.Parallel()
	body := `{
		"data": [
			{"id": "run-1", "status": "Finished", "phase": "completed"},
			{"id": "run-2", "status": "Finished", "phase": "completed"}
		],
		"first_id": "run-1",
		"last_id": "run-2",
		"has_more": true
	}`

	var page PagedRoutineRun
	require.NoError(t, json.Unmarshal([]byte(body), &page))

	items := page.Items()
	require.Len(t, items, 2)
	assert.Equal(t, "run-1", items[0].ID)
	assert.Equal(t, "Finished", items[0].Status)
	assert.Equal(t, "completed", items[0].Phase)
	assert.Equal(t, "run-2", page.NextCursor())
}

// TestPagedRoutineRun_LegacyEnvelope keeps the run-list endpoint working on
// regions that still emit the previous `{ value, nextPageToken }` shape.
func TestPagedRoutineRun_LegacyEnvelope(t *testing.T) {
	t.Parallel()
	body := `{
		"value": [
			{"id": "legacy-run", "status": "Finished"}
		],
		"nextPageToken": "opaque-token"
	}`

	var page PagedRoutineRun
	require.NoError(t, json.Unmarshal([]byte(body), &page))

	items := page.Items()
	require.Len(t, items, 1)
	assert.Equal(t, "legacy-run", items[0].ID)
	assert.Equal(t, "opaque-token", page.NextCursor(),
		"NextCursor must fall back to nextPageToken on the legacy envelope")
}

// TestPagedRoutineRun_PrefersDataOverValue guards against a transitional
// service that returns both envelopes simultaneously.
func TestPagedRoutineRun_PrefersDataOverValue(t *testing.T) {
	t.Parallel()
	body := `{
		"data": [{"id": "spec"}],
		"value": [{"id": "legacy"}]
	}`

	var page PagedRoutineRun
	require.NoError(t, json.Unmarshal([]byte(body), &page))

	items := page.Items()
	require.Len(t, items, 1)
	assert.Equal(t, "spec", items[0].ID)
}
