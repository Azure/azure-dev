// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunJSONPreservesUnknownServiceFields(t *testing.T) {
	const response = `{
		"id":"run_1","status":"completed",
		"future_counter":9007199254740993,
		"future_diagnostics":{"available":false,"reason":null,"counts":[1,2]},
		"data_source":{"type":"azure_ai_user_conversation_simulation_preview",
			"default_simulation_configuration":{"conversation_repetitions":3,
				"enable_conversation_dataset_generation":false,"future_setting":"kept"},
			"source":{"type":"file_id","id":"service-issued-id","future_id":"also-kept"}},
		"result_counts":{"total":4,"passed":2,"failed":1,"errored":1,"skipped":0,"future_count":42},
		"per_testing_criteria_results":[{"testing_criteria":"quality",
			"passed":2,"failed":1,"errored":1,"skipped":0,"future_metric":{"raw":7}}]
	}`
	client, _ := recorder(t, http.StatusOK, response)
	run, err := client.GetOpenAIEvalRun(t.Context(), "eval_1", "run_1")
	require.NoError(t, err)
	run.PortalURL = "https://ai.azure.com/run_1"
	body, err := json.Marshal(run)
	require.NoError(t, err)
	var want, got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(response), &want))
	require.NoError(t, json.Unmarshal(body, &got))
	for key, value := range want {
		assert.JSONEq(t, string(value), string(got[key]), key)
	}
	assert.JSONEq(t, `"https://ai.azure.com/run_1"`, string(got["portal_url"]))

	run.Status = "failed"
	body, err = json.Marshal(run)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(body, &got))
	assert.JSONEq(t, `"failed"`, string(got["status"]))
	assert.JSONEq(t, "9007199254740993", string(got["future_counter"]))
}

func TestRunJSONLegacyAndConstructedShapes(t *testing.T) {
	for _, response := range []string{
		`{"id":"old_run","status":"completed"}`,
		`{"id":"empty_run"}`,
		`{"id":"static","evaluation_level":"conversation","data_source":{"type":"jsonl"}}`,
	} {
		var run OpenAIEvalRun
		require.NoError(t, json.Unmarshal([]byte(response), &run))
		body, err := json.Marshal(run)
		require.NoError(t, err)
		assert.JSONEq(t, response, string(body))
	}
	body, err := json.Marshal(OpenAIEvalRun{ID: "new_run"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"new_run"}`, string(body))
}

func TestRunJSONDoesNotLeakSubmissionOnlySeedCount(t *testing.T) {
	source := NewSimulationDataSource("agent", "model", 1, 0)
	source.SimulationSeedCount = new(2)
	body, err := json.Marshal(CreateOpenAIEvalRunRequest{Name: "simulation", DataSource: source})
	require.NoError(t, err)
	assert.NotContains(t, string(body), "SimulationSeedCount")
	assert.NotContains(t, string(body), "seed_count")
	assert.NotContains(t, string(body), "max_num_turns")
}

func TestRunJSONPreservesResultCountPresence(t *testing.T) {
	for _, counts := range []string{
		`null`,
		`{}`,
		`{"total":2}`,
		`{"total":2,"passed":null,"failed":null,"errored":1}`,
		`{"total":0,"passed":0,"failed":0,"errored":0,"skipped":0}`,
		`{"total":null,"passed":null,"failed":null,"errored":null,"skipped":null}`,
		`{"total":2,"failed":null,"future_count":9007199254740993}`,
	} {
		t.Run(counts, func(t *testing.T) {
			response := `{"id":"run_partial","result_counts":` + counts + `}`
			var run OpenAIEvalRun
			require.NoError(t, json.Unmarshal([]byte(response), &run))
			before := run.ReportedResultCounts()
			encoded, err := json.Marshal(run)
			require.NoError(t, err)
			assert.JSONEq(t, response, string(encoded))

			var decoded OpenAIEvalRun
			require.NoError(t, json.Unmarshal(encoded, &decoded))
			assert.Equal(t, before, decoded.ReportedResultCounts(), "a JSON round trip must not invent reported counters")
		})
	}
}

func TestRunJSONUpdatesReportedCountsWithoutOverwritingUnknownCounts(t *testing.T) {
	var run OpenAIEvalRun
	require.NoError(t, json.Unmarshal([]byte(`{
		"id":"run_partial","result_counts":{"total":2,"passed":1,"failed":null,"future_count":7}
	}`), &run))
	run.ResultCounts.Passed = 2
	run.PortalURL = "https://ai.azure.com/run_partial"
	encoded, err := json.Marshal(run)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"id":"run_partial","result_counts":{"total":2,"passed":2,"failed":null,"future_count":7},
		"portal_url":"https://ai.azure.com/run_partial"
	}`, string(encoded))
}
