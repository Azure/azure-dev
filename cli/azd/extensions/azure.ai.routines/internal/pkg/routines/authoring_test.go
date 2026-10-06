// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package routines

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAuthoringMap_CamelCaseProperties(t *testing.T) {
	t.Parallel()

	routine, err := ParseAuthoringMap(map[string]any{
		"triggers": map[string]any{
			"default": map[string]any{
				"type":           "schedule",
				"cronExpression": "0 9 * * *",
				"timeZone":       "UTC",
				"connectionId":   "connection",
				"issueEvent":     "opened",
				"eventName":      "event.created",
			},
		},
		"action": map[string]any{
			"type":            "invoke_agent_invocations_api",
			"agentName":       "agent",
			"agentEndpointId": "endpoint",
			"sessionId":       "session",
		},
	})
	require.NoError(t, err)

	trigger := routine.Triggers["default"]
	assert.Equal(t, "0 9 * * *", trigger.CronExpression)
	assert.Equal(t, "UTC", trigger.TimeZone)
	assert.Equal(t, "connection", trigger.ConnectionID)
	assert.Equal(t, "opened", trigger.IssueEvent)
	assert.Equal(t, "event.created", trigger.EventName)
	require.NotNil(t, routine.Action)
	assert.Equal(t, "agent", routine.Action.AgentName)
	assert.Equal(t, "endpoint", routine.Action.AgentEndpointID)
	assert.Equal(t, "session", routine.Action.SessionID)
}

func TestValidateAuthoringKeys_RejectsRetiredSnakeCaseProperties(t *testing.T) {
	t.Parallel()

	tests := []struct {
		key         string
		replacement string
		action      bool
	}{
		{key: "cron_expression", replacement: "cronExpression"},
		{key: "time_zone", replacement: "timeZone"},
		{key: "connection_id", replacement: "connectionId"},
		{key: "issue_event", replacement: "issueEvent"},
		{key: "event_name", replacement: "eventName"},
		{key: "agent_name", replacement: "agentName", action: true},
		{key: "agent_endpoint_id", replacement: "agentEndpointId", action: true},
		{key: "session_id", replacement: "sessionId", action: true},
	}

	for _, test := range tests {
		t.Run(test.key, func(t *testing.T) {
			t.Parallel()

			values := map[string]any{}
			if test.action {
				values["action"] = map[string]any{"type": "action", test.key: "value"}
			} else {
				values["triggers"] = map[string]any{
					"default": map[string]any{"type": "trigger", test.key: "value"},
				}
			}

			err := ValidateAuthoringKeys(values)
			require.Error(t, err)
			assert.ErrorContains(t, err, test.key)
			assert.ErrorContains(t, err, test.replacement)
		})
	}
}

func TestParseAuthoringMap_PreservesPassThroughPayloadProperties(t *testing.T) {
	t.Parallel()

	values := map[string]any{
		"triggers": map[string]any{
			"default": map[string]any{
				"type": "custom",
				"parameters": map[string]any{
					"cron_expression": "provider-owned",
					"session_id":      "provider-session",
				},
			},
		},
		"action": map[string]any{
			"type":      "invoke_agent_responses_api",
			"agentName": "agent",
			"input": map[string]any{
				"agent_name":    "payload-agent",
				"event_name":    "payload-event",
				"connection_id": "payload-connection",
			},
		},
	}

	routine, err := ParseAuthoringMap(values)
	require.NoError(t, err)
	assert.Equal(t, "provider-owned", (*routine.Triggers["default"].Parameters)["cron_expression"])
	require.NotNil(t, routine.Action)
	action, ok := values["action"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, action["input"], routine.Action.Input)
}

func TestParseAuthoringJSONAndYAML_UseCamelCase(t *testing.T) {
	t.Parallel()

	jsonRoutine, err := ParseAuthoringJSON([]byte(`{
		"triggers":{"default":{"type":"schedule","cronExpression":"0 9 * * *"}},
		"action":{"type":"invoke_agent_responses_api","agentName":"agent"}
	}`))
	require.NoError(t, err)
	assert.Equal(t, "0 9 * * *", jsonRoutine.Triggers["default"].CronExpression)
	assert.Equal(t, "agent", jsonRoutine.Action.AgentName)

	yamlRoutine, err := ParseAuthoringYAML([]byte(`
triggers:
  default:
    type: schedule
    cronExpression: "0 9 * * *"
action:
  type: invoke_agent_responses_api
  agentName: agent
`))
	require.NoError(t, err)
	assert.Equal(t, "0 9 * * *", yamlRoutine.Triggers["default"].CronExpression)
	assert.Equal(t, "agent", yamlRoutine.Action.AgentName)
}
