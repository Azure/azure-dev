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

func TestBuildTrigger_RecurringDeferred(t *testing.T) {
	t.Parallel()
	// `recurring` is in TriggerCLIToWire but is deferred at the CLI surface
	// (service-side schedule create is not yet ready); buildTrigger must
	// reject it explicitly with a "deferred" message.
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
	got, err := buildAction("agent-response", "my-agent-id", "", "conv-1", "")
	require.NoError(t, err)
	assert.Equal(t, routines.ActionCLIToWire["agent-response"], got.Type)
	assert.Equal(t, "my-agent-id", got.AgentID)
	assert.Empty(t, got.AgentEndpointID)
	assert.Equal(t, "conv-1", got.ConversationID)
}

func TestBuildAction_AgentResponseByEndpointID(t *testing.T) {
	t.Parallel()
	got, err := buildAction("agent-response", "", "ep-id-123", "", "")
	require.NoError(t, err)
	assert.Empty(t, got.AgentID)
	assert.Equal(t, "ep-id-123", got.AgentEndpointID)
}

func TestBuildAction_AgentResponseMutuallyExclusive(t *testing.T) {
	t.Parallel()
	_, err := buildAction("agent-response", "my-agent-id", "ep-id-123", "", "")
	assert.Error(t, err, "agent-id and agent-endpoint-id must be mutually exclusive")
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
