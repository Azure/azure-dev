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
	assert.Equal(t, "2026-04-24T15:00:00Z", got.At.String())
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

func TestBuildTrigger_GithubIssue(t *testing.T) {
	t.Parallel()
	flags := &routineCreateFlags{
		trigger:      "github-issue",
		connectionID: "conn-1",
		owner:        "octocat",
		repository:   "hello-world",
		issueEvent:   "opened",
	}
	got, err := buildTrigger(flags)
	require.NoError(t, err)
	assert.Equal(t, "github_issue", got.Type)
	assert.Equal(t, "octocat", got.Owner)
	assert.Equal(t, "opened", got.IssueEvent)
}

func TestBuildTrigger_GithubIssueMissingFields(t *testing.T) {
	t.Parallel()
	flags := &routineCreateFlags{trigger: "github-issue", owner: "octocat"}
	_, err := buildTrigger(flags)
	assert.Error(t, err)
}

func TestBuildTrigger_GithubIssueInvalidEvent(t *testing.T) {
	t.Parallel()
	flags := &routineCreateFlags{
		trigger:      "github-issue",
		connectionID: "c", owner: "o", repository: "r",
		issueEvent: "edited",
	}
	_, err := buildTrigger(flags)
	assert.Error(t, err)
}

func TestBuildTrigger_Custom(t *testing.T) {
	t.Parallel()
	flags := &routineCreateFlags{
		trigger:        "custom",
		provider:       "stripe",
		eventName:      "charge.succeeded",
		parametersJSON: `{"amount":1000}`,
	}
	got, err := buildTrigger(flags)
	require.NoError(t, err)
	assert.Equal(t, "custom", got.Type)
	assert.Equal(t, "stripe", got.Provider)
	assert.Equal(t, "charge.succeeded", got.EventName)
	assert.Equal(t, float64(1000), (*got.Parameters)["amount"])
}

func TestBuildTrigger_CustomMissingProvider(t *testing.T) {
	t.Parallel()
	flags := &routineCreateFlags{trigger: "custom"}
	_, err := buildTrigger(flags)
	assert.Error(t, err)
}

func TestBuildTrigger_CustomMissingParameters(t *testing.T) {
	t.Parallel()
	flags := &routineCreateFlags{trigger: "custom", provider: "stripe"}
	_, err := buildTrigger(flags)
	assert.Error(t, err)
}

func TestBuildTrigger_CustomBadParametersJSON(t *testing.T) {
	t.Parallel()
	flags := &routineCreateFlags{
		trigger: "custom", provider: "p", parametersJSON: "not-json",
	}
	_, err := buildTrigger(flags)
	assert.Error(t, err)
}

func TestBuildTrigger_TimerRejectsTimeZone(t *testing.T) {
	t.Parallel()
	flags := &routineCreateFlags{
		trigger: "timer", at: "2026-04-24T15:00:00Z", timeZone: "America/New_York",
	}
	_, err := buildTrigger(flags)
	assert.Error(t, err, "timer no longer accepts time_zone in the v1 spec")
}

// ─── buildAction ──────────────────────────────────────────────────────────────

func TestBuildAction_AgentResponseByID(t *testing.T) {
	t.Parallel()
	got, err := buildAction("agent-response", "my-agent-name", "", "conv-1", "")
	require.NoError(t, err)
	assert.Equal(t, routines.ActionCLIToWire["agent-response"], got.Type)
	assert.Equal(t, "my-agent-name", got.AgentName)
	assert.Empty(t, got.AgentEndpointID)
	assert.Equal(t, "conv-1", got.Conversation)
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

func TestBuildAction_AgentInvokeByEndpoint(t *testing.T) {
	t.Parallel()
	got, err := buildAction("agent-invoke", "", "ep-id-456", "", "sess-1")
	require.NoError(t, err)
	assert.Equal(t, routines.ActionCLIToWire["agent-invoke"], got.Type)
	assert.Equal(t, "ep-id-456", got.AgentEndpointID)
	assert.Equal(t, "sess-1", got.SessionID)
}

// Spec PR #43498: agent-invoke now also accepts agent_name (shared field set
// with agent-response).
func TestBuildAction_AgentInvokeByName(t *testing.T) {
	t.Parallel()
	got, err := buildAction("agent-invoke", "my-agent", "", "", "sess-1")
	require.NoError(t, err)
	assert.Equal(t, "my-agent", got.AgentName)
	assert.Empty(t, got.AgentEndpointID)
}

func TestBuildAction_AgentInvokeMutuallyExclusive(t *testing.T) {
	t.Parallel()
	_, err := buildAction("agent-invoke", "my-agent", "ep-id", "", "")
	assert.Error(t, err)
}

func TestBuildAction_AgentInvokeMissingBoth(t *testing.T) {
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

func TestBuildAction_AgentInvokeRejectsConversationID(t *testing.T) {
	t.Parallel()
	_, err := buildAction("agent-invoke", "", "ep-id", "conv-1", "")
	assert.Error(t, err, "--conversation-id must be rejected for agent-invoke action")
}
