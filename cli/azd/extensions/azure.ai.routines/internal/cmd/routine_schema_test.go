// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRoutineSchemaUsesCamelCaseAuthoringKeys(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "azure.ai.routine.json"))
	require.NoError(t, err)

	var schema map[string]any
	require.NoError(t, json.Unmarshal(data, &schema))

	properties := requireSchemaMap(t, schema, "properties")
	authorization := requireSchemaMap(t, properties, "authorization")
	require.Equal(t, []any{"identity"}, authorization["required"])
	authorizationProperties := requireSchemaMap(t, authorization, "properties")
	identity := requireSchemaMap(t, authorizationProperties, "identity")
	require.Equal(t, []any{"agent", "creator"}, identity["enum"])

	triggers := requireSchemaMap(t, properties, "triggers")
	triggerItem := requireSchemaMap(t, triggers, "additionalProperties")
	triggerProperties := requireSchemaMap(t, triggerItem, "properties")
	for _, key := range []string{
		"cronExpression", "timeZone", "connectionId", "issueEvent", "eventName",
	} {
		require.Contains(t, triggerProperties, key)
	}
	for _, key := range []string{
		"cron_expression", "time_zone", "connection_id", "issue_event", "event_name",
	} {
		require.NotContains(t, triggerProperties, key)
	}
	require.ElementsMatch(t,
		[]string{"cron_expression", "time_zone", "connection_id", "issue_event", "event_name"},
		schemaRejectedRequiredKeys(t, triggerItem),
	)

	action := requireSchemaMap(t, properties, "action")
	actionProperties := requireSchemaMap(t, action, "properties")
	for _, key := range []string{"agentName", "agentEndpointId", "sessionId"} {
		require.Contains(t, actionProperties, key)
	}
	for _, key := range []string{"agent_name", "agent_endpoint_id", "session_id"} {
		require.NotContains(t, actionProperties, key)
	}
	require.ElementsMatch(t,
		[]string{"agent_name", "agent_endpoint_id", "session_id"},
		schemaRejectedRequiredKeys(t, action),
	)
}

func requireSchemaMap(t *testing.T, value map[string]any, key string) map[string]any {
	t.Helper()
	raw, found := value[key]
	require.True(t, found, "schema key %q is missing", key)
	result, ok := raw.(map[string]any)
	require.True(t, ok, "schema key %q is not an object", key)
	return result
}

func schemaRejectedRequiredKeys(t *testing.T, value map[string]any) []string {
	t.Helper()
	notSchema := requireSchemaMap(t, value, "not")
	rawAnyOf, found := notSchema["anyOf"]
	require.True(t, found)
	anyOf, ok := rawAnyOf.([]any)
	require.True(t, ok)

	keys := make([]string, 0, len(anyOf))
	for _, raw := range anyOf {
		entry, ok := raw.(map[string]any)
		require.True(t, ok)
		rawRequired, found := entry["required"]
		require.True(t, found)
		required, ok := rawRequired.([]any)
		require.True(t, ok)
		require.Len(t, required, 1)
		key, ok := required[0].(string)
		require.True(t, ok)
		keys = append(keys, key)
	}
	return keys
}
