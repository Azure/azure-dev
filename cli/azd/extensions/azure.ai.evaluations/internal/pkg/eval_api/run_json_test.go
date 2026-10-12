// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"encoding/json"
	"fmt"
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

func TestRunJSONPreservesNestedCriteriaPresence(t *testing.T) {
	for _, criterion := range []string{
		`{"passed":1,"failed":null}`,
		`{"testing_criteria":"quality"}`,
		`{"testing_criteria":"quality","passed":null,"failed":null,"errored":null,"skipped":null}`,
		`{"testing_criteria":"quality","passed":0,"failed":0,"errored":0,"skipped":0}`,
		`{"testing_criteria":"quality","passed":1,"unknown":{"number":9007199254740993,"decimal":0.1234567890123456789}}`,
	} {
		t.Run(criterion, func(t *testing.T) {
			response := `{"id":"run_presence","per_testing_criteria_results":[` + criterion + `]}`
			var run OpenAIEvalRun
			require.NoError(t, json.Unmarshal([]byte(response), &run))
			body, err := json.Marshal(run)
			require.NoError(t, err)
			var doc struct {
				Criteria []map[string]json.RawMessage `json:"per_testing_criteria_results"`
			}
			require.NoError(t, json.Unmarshal(body, &doc))
			var want map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(criterion), &want))
			require.Len(t, doc.Criteria, 1)
			assert.Equal(t, want, doc.Criteria[0], "no added zero counters, and exact raw values survive")
		})
	}
}

func TestRunJSONNestedCounterChangesPreserveOtherPresence(t *testing.T) {
	var run OpenAIEvalRun
	require.NoError(t, json.Unmarshal([]byte(`{"id":"run_presence","per_testing_criteria_results":[
		{"testing_criteria":"quality","passed":1,"failed":null,"future":9007199254740993}
	]}`), &run))
	run.PerTestingCriteria[0].Passed = 0
	run.PerTestingCriteria[0].Errored = 2
	body, err := json.Marshal(run)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"run_presence","per_testing_criteria_results":[
		{"testing_criteria":"quality","passed":0,"failed":null,"errored":2,"future":9007199254740993}
	]}`, string(body))
	assert.Contains(t, string(body), "9007199254740993")
}

func TestRunJSONLegacyAndConstructedShapes(t *testing.T) {
	for _, response := range []string{
		`{"status":"completed"}`,
		`{"id":null,"status":"completed"}`,
		`{"id":"","status":"completed"}`,
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

func TestRunJSONPreservesExactSourceNumbers(t *testing.T) {
	for _, value := range []string{
		"9007199254740993", "-9007199254740993", "18446744073709551615",
		"0.12345678901234567890123456789", "1e400", "0",
	} {
		for _, nested := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/nested=%t", value, nested), func(t *testing.T) {
				source := `{"type":"file_content","content":[{"value":` + value +
					`,"nested":[{"value":` + value + `}],"empty":null,"zero":0}],"unknown":` + value + `}`
				dataSource := `"source":` + source
				if nested {
					dataSource = `"item_generation_params":{"type":"response_retrieval","source":` + source + `}`
				}
				response := `{"id":"run_numbers","data_source":{"type":"jsonl",` + dataSource + `}}`
				var run OpenAIEvalRun
				require.NoError(t, json.Unmarshal([]byte(response), &run))
				require.NotNil(t, run.DataSource)
				content := run.DataSource.Source
				if nested {
					require.NotNil(t, run.DataSource.ItemGenerationParams)
					content = run.DataSource.ItemGenerationParams.Source
				}
				require.NotNil(t, content)
				require.Len(t, content.Content, 1)
				assert.Equal(t, json.Number(value), content.Content[0]["value"])
				run.Status = "completed"
				body, err := json.Marshal(run)
				require.NoError(t, err)
				assert.Contains(t, string(body), `"value":`+value)
				assert.Contains(t, string(body), `"nested":[{"value":`+value+`}]`)
				assert.Contains(t, string(body), `"unknown":`+value)
				assert.Contains(t, string(body), `"empty":null`)
				assert.Contains(t, string(body), `"zero":0`)
				assert.Contains(t, string(body), `"status":"completed"`)
			})
		}
	}
}

func TestRunDecoderRejectsMalformedAndTrailingData(t *testing.T) {
	for _, input := range []string{
		`{"id":"1"`, `{"id":"1"}{"id":"2"}`, `{"id":"1"} true`,
		`{"id":"1"} garbage`, `{"id":"1","created_at":1e}`,
	} {
		run := OpenAIEvalRun{ID: "unchanged"}
		require.Error(t, run.UnmarshalJSON([]byte(input)))
		assert.Equal(t, "unchanged", run.ID)
	}
}
