// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"encoding/json"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestStandbyPoolPresenceAndMapping(t *testing.T) {
	for _, test := range []struct {
		name string
		json string
		yaml string
		size *int32
	}{
		{"omitted", `{}`, `{}`, nil},
		{"null endpoint", `{"agentEndpoint":null}`, "agent_endpoint: null", nil},
		{"null pool", `{"agentEndpoint":{"standbyPool":null}}`, "agent_endpoint:\n  standby_pool: null", nil},
		{"empty pool", `{"agentEndpoint":{"standbyPool":{}}}`, "agent_endpoint:\n  standby_pool: {}", nil},
		{"null size", `{"agentEndpoint":{"standbyPool":{"size":null}}}`, "agent_endpoint:\n  standby_pool:\n    size: null", nil},
		{"zero", `{"agentEndpoint":{"standbyPool":{"size":0}}}`, "agent_endpoint:\n  standby_pool:\n    size: 0", new(int32(0))},
		{"positive", `{"agentEndpoint":{"standbyPool":{"size":3}}}`, "agent_endpoint:\n  standby_pool:\n    size: 3", new(int32(3))},
		{"maximum", `{"agentEndpoint":{"standbyPool":{"size":50}}}`, "agent_endpoint:\n  standby_pool:\n    size: 50", new(int32(50))},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, format := range []string{"json", "yaml"} {
				t.Run(format, func(t *testing.T) {
					var definition ContainerAgent
					if format == "json" {
						require.NoError(t, json.Unmarshal([]byte(test.json), &definition))
					} else {
						require.NoError(t, yaml.Unmarshal([]byte(test.yaml), &definition))
					}
					endpoint, card, err := MapEndpointAndCard(definition.AgentEndpoint, nil)
					require.NoError(t, err)
					require.Nil(t, card)
					data, err := json.Marshal(&agent_api.PatchAgentRequest{AgentEndpoint: endpoint})
					require.NoError(t, err)
					if test.size == nil {
						require.NotContains(t, string(data), "standby_pool")
						return
					}
					require.NotNil(t, endpoint.StandbyPool)
					require.Equal(t, *test.size, *endpoint.StandbyPool.Size)
					require.JSONEq(t, `{"agent_endpoint":{"standby_pool":{"size":`+
						jsonNumber(t, test.size)+`}}}`, string(data))
					created, err := createAgentAPIRequest(AgentDefinition{Name: "test"}, map[string]any{
						"kind": "hosted",
					}, definition.AgentEndpoint, nil)
					require.NoError(t, err)
					require.Equal(t, endpoint, created.AgentEndpoint)
					payload, err := json.Marshal(created)
					require.NoError(t, err)
					var fields map[string]json.RawMessage
					require.NoError(t, json.Unmarshal(payload, &fields))
					require.NotContains(t, string(fields["definition"]), "standby_pool")
				})
			}
		})
	}
}

func jsonNumber(t *testing.T, value *int32) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}

func TestStandbyPoolRejectsInvalidConfiguration(t *testing.T) {
	for _, value := range []string{`-1`, `51`, `3.5`, `"3"`, `true`, `2147483648`, `[]`, `{}`} {
		t.Run(value, func(t *testing.T) {
			var pool StandbyPool
			require.Error(t, json.Unmarshal([]byte(`{"size":`+value+`}`), &pool))
			require.Error(t, yaml.Unmarshal([]byte("size: "+value), &pool))
		})
	}
	for _, value := range []string{`{"refill":true}`, `{"size":3,"refill":false}`, `[]`, `"3"`} {
		var pool StandbyPool
		require.Error(t, json.Unmarshal([]byte(value), &pool))
		require.Error(t, yaml.Unmarshal([]byte(value), &pool))
	}
	for _, size := range []int32{-1, 51} {
		_, _, err := MapEndpointAndCard(&AgentEndpoint{StandbyPool: &StandbyPool{Size: new(size)}}, nil)
		require.ErrorContains(t, err, "between 0 and 50")
	}
}
