// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponsesSourceUsesRequiredItemEnvelope(t *testing.T) {
	ids := []string{"resp_fixed", "resp_\"quoted\\id"}
	source := NewResponsesDataSource(ids, 1)
	raw, err := json.Marshal(source)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"type":"azure_ai_responses",
		"item_generation_params":{
			"type":"response_retrieval","max_num_turns":1,
			"data_mapping":{"response_id":"{{item.response_id}}"},
			"source":{"type":"file_content","content":[
				{"item":{"response_id":"resp_fixed"}},
				{"item":{"response_id":"resp_\"quoted\\id"}}
			]}
		}
	}`, string(raw))
	require.Equal(t, []string{"resp_fixed", "resp_\"quoted\\id"}, ids)
	require.Nil(t, source.Target)
	require.Nil(t, source.InputMessages)
}

func TestResponseConfigOmitsCustomFieldsOnlyForScenario(t *testing.T) {
	for _, tc := range []struct {
		config DataSourceConfig
		want   string
	}{
		{DataSourceConfig{Type: "azure_ai_source", Scenario: "responses"},
			`{"type":"azure_ai_source","scenario":"responses"}`},
		{DataSourceConfig{Type: "custom", ItemSchema: map[string]any{"type": "object"}},
			`{"type":"custom","item_schema":{"type":"object"},"include_sample_schema":false}`},
	} {
		raw, err := json.Marshal(tc.config)
		require.NoError(t, err)
		require.JSONEq(t, tc.want, string(raw))
		var decoded DataSourceConfig
		require.NoError(t, json.Unmarshal(raw, &decoded))
		require.Equal(t, tc.config, decoded)
	}
}

func TestResponseEnvelopeDoesNotChangeOtherInlineSources(t *testing.T) {
	rows := []map[string]any{{"query": "unchanged"}}
	for _, source := range []*EvalRunDataSource{
		NewDatasetOnlyDataSource(),
		NewAgentTargetDataSource("agent", nil),
		NewModelTargetDataSource("model"),
	} {
		source.SetFileContent(rows)
		require.Equal(t, rows, source.Source.Content)
		require.NotContains(t, source.Source.Content[0], "item")
	}
}
