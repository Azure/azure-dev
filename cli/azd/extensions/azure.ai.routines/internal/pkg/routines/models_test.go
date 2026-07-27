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
		"custom":       "custom",
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
		case "recurring", "timer", "github-issue", "custom":
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

// PagedRoutine now uses continuationToken instead of nextLink (spec PR #43498).
func TestPagedRoutine_ContinuationToken(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"value":[{"name":"r1"}],"continuationToken":"abc123"}`)
	var page PagedRoutine
	require.NoError(t, json.Unmarshal(raw, &page))
	require.Len(t, page.Value, 1)
	assert.Equal(t, "r1", page.Value[0].Name)
	assert.Equal(t, "abc123", page.ContinuationToken)
}

// RoutineDispatchPayload.Input is now any (can be object/array/scalar/null).
func TestRoutineDispatchPayload_InputAny(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input any
		want  string
	}{
		{"string", "hello", `"hello"`},
		{"number", 42.0, `42`},
		{"bool", true, `true`},
		{"object", map[string]any{"k": "v"}, `{"k":"v"}`},
		{"array", []any{1.0, 2.0}, `[1,2]`},
		{"nil-omitted", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := RoutineDispatchPayload{Type: "invoke_agent_responses_api", Input: tc.input}
			data, err := json.Marshal(p)
			require.NoError(t, err)
			if tc.want == "" {
				// nil should be omitted via omitempty
				assert.NotContains(t, string(data), `"input"`)
				return
			}
			assert.Contains(t, string(data), `"input":`+tc.want)
		})
	}
}

// RoutineRun gained several optional fields; ensure they round-trip.
func TestRoutineRun_NewFieldsRoundTrip(t *testing.T) {
	t.Parallel()
	status := int32(500)
	raw := []byte(`{
		"id":"run-1",
		"trigger_name":"default",
		"agent_id":"agent-a",
		"agent_endpoint_id":"ep-1",
		"conversation_id":"conv-1",
		"session_id":"sess-1",
		"scheduled_fire_at":"2026-01-01T00:00:00Z",
		"error_status_code":500
	}`)
	var run RoutineRun
	require.NoError(t, json.Unmarshal(raw, &run))
	assert.Equal(t, "default", run.TriggerName)
	assert.Equal(t, "agent-a", run.AgentID)
	assert.Equal(t, "ep-1", run.AgentEndpointID)
	assert.Equal(t, "conv-1", run.ConversationID)
	assert.Equal(t, "sess-1", run.SessionID)
	assert.Equal(t, "2026-01-01T00:00:00Z", run.ScheduledFireAt.String())
	require.NotNil(t, run.ErrorStatusCode)
	assert.Equal(t, status, *run.ErrorStatusCode)
}

// github_issue trigger now uses owner + issue_event instead of assignee.
func TestRoutineTrigger_GitHubIssueFields(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
		"type":"github_issue",
		"connection_id":"conn-1",
		"owner":"octocat",
		"repository":"hello-world",
		"issue_event":"opened"
	}`)
	var trig RoutineTrigger
	require.NoError(t, json.Unmarshal(raw, &trig))
	assert.Equal(t, "github_issue", trig.Type)
	assert.Equal(t, "octocat", trig.Owner)
	assert.Equal(t, "opened", trig.IssueEvent)
	assert.Equal(t, GitHubIssueEventOpened, trig.IssueEvent)
}

// New custom trigger carries provider/event_name/parameters.
func TestRoutineTrigger_CustomFields(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
		"type":"custom",
		"provider":"my-provider",
		"event_name":"my-event",
		"parameters":{"foo":"bar","n":1}
	}`)
	var trig RoutineTrigger
	require.NoError(t, json.Unmarshal(raw, &trig))
	assert.Equal(t, "custom", trig.Type)
	assert.Equal(t, "my-provider", trig.Provider)
	assert.Equal(t, "my-event", trig.EventName)
	assert.Equal(t, "bar", (*trig.Parameters)["foo"])
}

// RoutineAction.Conversation replaces the old conversation_id wire field.
func TestRoutineAction_ConversationField(t *testing.T) {
	t.Parallel()
	a := RoutineAction{Type: "invoke_agent_responses_api", Conversation: "conv-1"}
	data, err := json.Marshal(a)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"conversation":"conv-1"`)
	assert.NotContains(t, string(data), `"conversation_id"`)
}

// FlexibleTimestamp must decode from a JSON string unchanged.
func TestFlexibleTimestamp_UnmarshalString(t *testing.T) {
	t.Parallel()
	var ts FlexibleTimestamp
	require.NoError(t, json.Unmarshal([]byte(`"2026-01-01T00:00:00Z"`), &ts))
	assert.Equal(t, "2026-01-01T00:00:00Z", ts.String())
}

// FlexibleTimestamp must decode a JSON number as Unix epoch seconds.
func TestFlexibleTimestamp_UnmarshalNumberSeconds(t *testing.T) {
	t.Parallel()
	var ts FlexibleTimestamp
	// 1735689600 == 2025-01-01T00:00:00Z
	require.NoError(t, json.Unmarshal([]byte(`1735689600`), &ts))
	assert.Equal(t, "2025-01-01T00:00:00Z", ts.String())
}

// FlexibleTimestamp must decode a large JSON number as Unix epoch milliseconds.
func TestFlexibleTimestamp_UnmarshalNumberMilliseconds(t *testing.T) {
	t.Parallel()
	var ts FlexibleTimestamp
	// 1735689600000 ms == 2025-01-01T00:00:00Z
	require.NoError(t, json.Unmarshal([]byte(`1735689600000`), &ts))
	assert.Equal(t, "2025-01-01T00:00:00Z", ts.String())
}

// FlexibleTimestamp always marshals back to a JSON string, preserving the
// request wire shape.
func TestFlexibleTimestamp_MarshalsAsString(t *testing.T) {
	t.Parallel()
	data, err := json.Marshal(FlexibleTimestamp("2026-04-24T15:00:00Z"))
	require.NoError(t, err)
	assert.Equal(t, `"2026-04-24T15:00:00Z"`, string(data))
}

// Regression for #8984: a Routine whose service response carries numeric
// created_at and timer triggers.<name>.at must decode without the
// "cannot unmarshal number into Go struct field ... of type string" error.
func TestRoutine_NumericTimestampsDecode(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
		"name":"my-schedule-routine",
		"created_at":1735689600,
		"triggers":{"default":{"type":"timer","at":1735689600}}
	}`)
	var r Routine
	require.NoError(t, json.Unmarshal(raw, &r))
	assert.Equal(t, "2025-01-01T00:00:00Z", r.CreatedAt.String())
	require.Contains(t, r.Triggers, "default")
	assert.Equal(t, "2025-01-01T00:00:00Z", r.Triggers["default"].At.String())
}
