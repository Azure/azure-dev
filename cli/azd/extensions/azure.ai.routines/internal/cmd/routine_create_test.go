// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azure.ai.routines/internal/pkg/routines"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── buildTrigger ─────────────────────────────────────────────────────────────

func TestBuildTrigger_Recurring(t *testing.T) {
	t.Parallel()
	flags := &routineCreateFlags{
		trigger:        "recurring",
		cronExpression: "0 8 * * *",
		timeZone:       "UTC",
	}
	got, err := buildTrigger(flags)
	require.NoError(t, err)
	assert.Equal(t, "schedule", got.Type)
	assert.Equal(t, "0 8 * * *", got.CronExpression)
	assert.Equal(t, "UTC", got.TimeZone)
}

func TestBuildTrigger_RecurringMissingCron(t *testing.T) {
	t.Parallel()
	flags := &routineCreateFlags{trigger: "recurring"}
	_, err := buildTrigger(flags)
	assert.Error(t, err)
}

func TestBuildTrigger_Timer(t *testing.T) {
	t.Parallel()
	flags := &routineCreateFlags{
		trigger:  "timer",
		at:       "2026-04-24T15:00:00Z",
		timeZone: "UTC",
	}
	got, err := buildTrigger(flags)
	require.NoError(t, err)
	assert.Equal(t, "timer", got.Type)
	assert.Equal(t, "2026-04-24T15:00:00Z", got.At)
}

func TestBuildTrigger_TimerMissingAt(t *testing.T) {
	t.Parallel()
	flags := &routineCreateFlags{trigger: "timer"}
	_, err := buildTrigger(flags)
	assert.Error(t, err)
}

// TestBuildTrigger_TimerInvalidAt covers issue #8421 Bug 6: `--at` accepts
// arbitrary strings today, and the CLI should reject obvious garbage with a
// clear local error instead of round-tripping a 400 through the service.
func TestBuildTrigger_TimerInvalidAt(t *testing.T) {
	t.Parallel()
	flags := &routineCreateFlags{
		trigger:  "timer",
		at:       "not-a-date",
		timeZone: "UTC",
	}
	_, err := buildTrigger(flags)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RFC 3339",
		"error message must reference RFC 3339 so users know what format is expected")
	assert.Contains(t, err.Error(), "not-a-date",
		"error message must echo back the invalid value the user provided")
}

// TestBuildTrigger_TimerAtVariants accepts every flavor that time.RFC3339
// parses so users can write standard datetimes with or without a numeric zone.
func TestBuildTrigger_TimerAtVariants(t *testing.T) {
	t.Parallel()
	for _, at := range []string{
		"2026-04-24T15:00:00Z",
		"2026-04-24T15:00:00+02:00",
		"2026-04-24T15:00:00-07:00",
	} {
		flags := &routineCreateFlags{trigger: "timer", at: at}
		got, err := buildTrigger(flags)
		require.NoErrorf(t, err, "RFC 3339 datetime %q should parse", at)
		assert.Equal(t, at, got.At)
	}
}

func TestBuildTrigger_UnknownType(t *testing.T) {
	t.Parallel()
	flags := &routineCreateFlags{trigger: "unknown-trigger"}
	_, err := buildTrigger(flags)
	assert.Error(t, err)
}

func TestBuildTrigger_GithubIssueRejected(t *testing.T) {
	t.Parallel()
	// "github-issue" is in TriggerCLIToWire but is deferred for v1; buildTrigger
	// must reject it explicitly rather than producing an incomplete trigger.
	flags := &routineCreateFlags{trigger: "github-issue"}
	_, err := buildTrigger(flags)
	assert.Error(t, err)
}

// ─── buildAction ──────────────────────────────────────────────────────────────

func TestBuildAction_AgentResponseByID(t *testing.T) {
	t.Parallel()
	got, err := buildAction("agent-response", "my-agent-name", "", "conv-1", "")
	require.NoError(t, err)
	assert.Equal(t, routines.ActionCLIToWire["agent-response"], got.Type)
	assert.Equal(t, "my-agent-name", got.AgentName)
	assert.Empty(t, got.AgentEndpointID)
	assert.Equal(t, "conv-1", got.ConversationID)
}

func TestBuildAction_AgentResponseByEndpointID(t *testing.T) {
	t.Parallel()
	got, err := buildAction("agent-response", "", "ep-id-123", "", "")
	require.NoError(t, err)
	assert.Empty(t, got.AgentName)
	assert.Equal(t, "ep-id-123", got.AgentEndpointID)
}

func TestBuildAction_AgentResponseMutuallyExclusive(t *testing.T) {
	t.Parallel()
	_, err := buildAction("agent-response", "my-agent-name", "ep-id-123", "", "")
	assert.Error(t, err, "agent-name and agent-endpoint-id must be mutually exclusive")
}

func TestBuildAction_AgentResponseMissingBoth(t *testing.T) {
	t.Parallel()
	_, err := buildAction("agent-response", "", "", "", "")
	assert.Error(t, err)
}

func TestBuildAction_AgentInvoke(t *testing.T) {
	t.Parallel()
	got, err := buildAction("agent-invoke", "", "ep-id-456", "", "sess-1")
	require.NoError(t, err)
	assert.Equal(t, routines.ActionCLIToWire["agent-invoke"], got.Type)
	assert.Equal(t, "ep-id-456", got.AgentEndpointID)
	assert.Equal(t, "sess-1", got.SessionID)
}

func TestBuildAction_AgentInvokeMissingEndpointID(t *testing.T) {
	t.Parallel()
	_, err := buildAction("agent-invoke", "", "", "", "")
	assert.Error(t, err)
}

func TestBuildAction_UnknownType(t *testing.T) {
	t.Parallel()
	_, err := buildAction("no-such-action", "", "ep", "", "")
	assert.Error(t, err)
}

func TestBuildAction_AgentResponseRejectsSessionID(t *testing.T) {
	t.Parallel()
	_, err := buildAction("agent-response", "my-agent-name", "", "", "sess-1")
	assert.Error(t, err, "--session-id must be rejected for agent-response action")
}

func TestBuildAction_AgentInvokeRejectsAgentName(t *testing.T) {
	t.Parallel()
	_, err := buildAction("agent-invoke", "my-agent-name", "ep-id", "", "")
	assert.Error(t, err, "--agent-name must be rejected for agent-invoke action")
}

func TestBuildAction_AgentInvokeRejectsConversationID(t *testing.T) {
	t.Parallel()
	_, err := buildAction("agent-invoke", "", "ep-id", "conv-1", "")
	assert.Error(t, err, "--conversation-id must be rejected for agent-invoke action")
}
