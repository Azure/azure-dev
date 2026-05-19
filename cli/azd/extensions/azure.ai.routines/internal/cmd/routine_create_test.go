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
	flags := &routineCreateFlags{
		trigger:  "recurring",
		cron:     "0 8 * * 1-5",
		timeZone: "America/New_York",
	}
	got, err := buildTrigger(flags)
	require.NoError(t, err)
	assert.Equal(t, "schedule", got.Type)
	assert.Equal(t, "0 8 * * 1-5", got.Cron)
	assert.Equal(t, "America/New_York", got.TimeZone)
}

func TestBuildTrigger_RecurringMissingCron(t *testing.T) {
	flags := &routineCreateFlags{trigger: "recurring"}
	_, err := buildTrigger(flags)
	assert.Error(t, err)
}

func TestBuildTrigger_Timer(t *testing.T) {
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
	flags := &routineCreateFlags{trigger: "timer"}
	_, err := buildTrigger(flags)
	assert.Error(t, err)
}

func TestBuildTrigger_UnknownType(t *testing.T) {
	flags := &routineCreateFlags{trigger: "unknown-trigger"}
	_, err := buildTrigger(flags)
	assert.Error(t, err)
}

// ─── buildAction ──────────────────────────────────────────────────────────────

func TestBuildAction_AgentResponseByName(t *testing.T) {
	got, err := buildAction("agent-response", "my-agent", "", "conv-1", "")
	require.NoError(t, err)
	assert.Equal(t, routines.ActionCLIToWire["agent-response"], got.Type)
	assert.Equal(t, "my-agent", got.AgentName)
	assert.Empty(t, got.AgentEndpointID)
	assert.Equal(t, "conv-1", got.ConversationID)
}

func TestBuildAction_AgentResponseByEndpointID(t *testing.T) {
	got, err := buildAction("agent-response", "", "ep-id-123", "", "")
	require.NoError(t, err)
	assert.Empty(t, got.AgentName)
	assert.Equal(t, "ep-id-123", got.AgentEndpointID)
}

func TestBuildAction_AgentResponseMutuallyExclusive(t *testing.T) {
	_, err := buildAction("agent-response", "my-agent", "ep-id-123", "", "")
	assert.Error(t, err, "agent-name and agent-endpoint-id must be mutually exclusive")
}

func TestBuildAction_AgentResponseMissingBoth(t *testing.T) {
	_, err := buildAction("agent-response", "", "", "", "")
	assert.Error(t, err)
}

func TestBuildAction_AgentInvoke(t *testing.T) {
	got, err := buildAction("agent-invoke", "", "ep-id-456", "", "sess-1")
	require.NoError(t, err)
	assert.Equal(t, routines.ActionCLIToWire["agent-invoke"], got.Type)
	assert.Equal(t, "ep-id-456", got.AgentEndpointID)
	assert.Equal(t, "sess-1", got.SessionID)
}

func TestBuildAction_AgentInvokeMissingEndpointID(t *testing.T) {
	_, err := buildAction("agent-invoke", "", "", "", "")
	assert.Error(t, err)
}

func TestBuildAction_UnknownType(t *testing.T) {
	_, err := buildAction("no-such-action", "", "ep", "", "")
	assert.Error(t, err)
}
