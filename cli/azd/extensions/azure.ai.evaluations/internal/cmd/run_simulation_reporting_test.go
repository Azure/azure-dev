// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func simulationReportingRun() *eval_api.OpenAIEvalRun {
	source := eval_api.NewSimulationDataSource("support-agent", "simulator", 3, 8)
	source.SimulationSeedCount = new(2)
	metadata := map[string]string{
		metaEvalName: "support", metaDataset: "seeds", metaDatasetVersion: "4",
	}
	recordSimulationMetadata(metadata, source)
	return &eval_api.OpenAIEvalRun{
		ID: "run_simulation", EvalID: "eval_simulation", Name: "nightly simulation", Status: "completed",
		DataSource: source, Metadata: metadata,
		ResultCounts: &eval_api.EvalRunResultCounts{Total: 4, Passed: 2, Failed: 1, Errored: 1},
		PerTestingCriteria: []eval_api.EvalRunCriteriaResult{
			{TestingCriteria: "quality", Passed: 2, Failed: 1, Errored: 1},
		},
	}
}

func TestSimulationReportingAtSummaryDetailAndOutputCallSites(t *testing.T) {
	for _, tc := range []struct {
		name   string
		render func(io.Writer, *eval_api.OpenAIEvalRun) error
	}{
		{"summary", func(out io.Writer, run *eval_api.OpenAIEvalRun) error { return renderRun(out, run, nil) }},
		{"detail", renderRunDetail},
		{"output", func(out io.Writer, run *eval_api.OpenAIEvalRun) error {
			return renderResults(out, run.EvalID, run, []eval_api.OutputItem{failingItem("row1")}, true)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			require.NoError(t, tc.render(&out, simulationReportingRun()))
			text := out.String()
			for _, expected := range []string{
				"Mode       conversation simulation",
				"Name       nightly simulation",
				"Dataset    seeds (version 4)",
				"SIMULATION CONFIGURATION (REQUESTED)",
				"Seed scenarios         2",
				"Repetitions per seed   3",
				"Maximum turns          8",
				"Conversations generated  not reported",
				"Conversations completed  not reported",
				"Conversation turns       not reported",
				"CONVERSATION EVALUATION RESULTS",
				"Total         4", "Failed        1", "Errored       1",
				"run output export",
			} {
				assert.Contains(t, text, expected)
			}
			assert.NotContains(t, text, "TEST CASE RESULTS")
			assert.NotContains(t, text, "generated  6", "two seeds times three repetitions is not an observed count")
			if tc.name != "output" {
				assert.Contains(t, text, "--failed-only")
			}
		})
	}
}

func TestSimulationReportingUsesRecordedSettingsWithoutLocalConfig(t *testing.T) {
	run := simulationReportingRun()
	run.DataSource = nil
	var out bytes.Buffer
	require.NoError(t, renderRunDetail(&out, run))
	assert.Contains(t, out.String(), "Repetitions per seed   3")
	assert.Contains(t, out.String(), "Maximum turns          8")

	run.DataSource = eval_api.NewSimulationDataSource("agent", "model", 1, 0)
	out.Reset()
	require.NoError(t, renderRunDetail(&out, run))
	assert.Contains(t, out.String(), "Repetitions per seed   3", "stored request is not replaced by service defaults")
	assert.Contains(t, out.String(), "Maximum turns          8")

	delete(run.Metadata, metaSimulationRepetitions)
	delete(run.Metadata, metaSimulationMaxTurns)
	out.Reset()
	require.NoError(t, renderRunDetail(&out, run))
	assert.Contains(t, out.String(), "Repetitions per seed   1", "older runs can use the recorded data source")
	assert.Contains(t, out.String(), "Maximum turns          service default (not specified)")
}

func TestSimulationReportingUnknownsAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name, seeds, repetitions, turns, wantSeeds, wantRepetitions, wantTurns string
	}{
		{"older simulation", "", "", "", "not reported", "not reported", "not reported"},
		{"minimum", "0", "1", "1", "0", "1", "1"},
		{"maximum", "2", "5", "20", "2", "5", "20"},
		{"service default", "2", "1", serviceDefaultTurns, "2", "1", "service default (not specified)"},
		{"invalid", "-1", "6", "21", "not reported", "not reported", "not reported"},
		{"zero", "0", "0", "0", "0", "not reported", "not reported"},
		{"malformed", "many", "1.5", "999999999999999999999", "not reported", "not reported", "not reported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := &eval_api.OpenAIEvalRun{ID: "run", Metadata: map[string]string{
				metaRunMode: simulationMode, metaSimulationSeeds: tc.seeds,
				metaSimulationRepetitions: tc.repetitions, metaSimulationMaxTurns: tc.turns,
			}}
			var out bytes.Buffer
			require.NoError(t, renderRun(&out, run, nil))
			assert.Contains(t, out.String(), "Seed scenarios         "+tc.wantSeeds)
			assert.Contains(t, out.String(), "Repetitions per seed   "+tc.wantRepetitions)
			assert.Contains(t, out.String(), "Maximum turns          "+tc.wantTurns)
			assert.Contains(t, out.String(), "Not reported by the service.")
			assert.Contains(t, out.String(), "Status     not reported")
		})
	}
}

func TestSimulationReportingDoesNotChangeOtherModes(t *testing.T) {
	for _, source := range []*eval_api.EvalRunDataSource{
		nil, {Type: eval_api.EvalRunDataSourceTypeJSONL}, {Type: eval_api.EvalRunDataSourceTypeAgentTarget},
	} {
		run := finishedRun()
		run.DataSource = source
		run.EvaluationLevel = "conversation"
		var out bytes.Buffer
		require.NoError(t, renderRun(&out, run, nil))
		assert.Contains(t, out.String(), "TEST CASE RESULTS")
		assert.NotContains(t, out.String(), "SIMULATION")
		metadata := map[string]string{"keep": "unchanged"}
		recordSimulationMetadata(metadata, source)
		assert.Equal(t, map[string]string{"keep": "unchanged"}, metadata)
		out.Reset()
		require.NoError(t, renderRunDetail(&out, run))
		assert.NotContains(t, out.String(), "SIMULATION")
		assert.Contains(t, out.String(), "Results")
	}
}

func TestSimulationReportingDoesNotAddDerivedJSONFields(t *testing.T) {
	run := simulationReportingRun()
	var out bytes.Buffer
	require.NoError(t, emitJSON(&out, run))
	var document map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out.Bytes(), &document))
	assert.NotContains(t, document, "conversations_generated")
	assert.NotContains(t, document, "conversation_turns")
	assert.Contains(t, document, "result_counts")
	var metadata map[string]string
	require.NoError(t, json.Unmarshal(document["metadata"], &metadata))
	assert.Equal(t, "2", metadata[metaSimulationSeeds])
	assert.Equal(t, "3", metadata[metaSimulationRepetitions])
	assert.Equal(t, "8", metadata[metaSimulationMaxTurns])
}

func TestSimulationSubmissionPersistsSettingsAndPreservesHandoff(t *testing.T) {
	for _, tc := range []struct {
		name string
		wait bool
		json bool
	}{
		{"async JSON handoff", false, true},
		{"waited JSON", true, true},
		{"waited summary", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var submitted eval_api.CreateOpenAIEvalRunRequest
			var response []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodPost:
					assert.NoError(t, json.NewDecoder(r.Body).Decode(&submitted))
					response, _ = json.Marshal(map[string]any{
						"id": "run_new", "eval_id": "eval_simulation", "status": "completed",
						"data_source": submitted.DataSource, "metadata": submitted.Metadata,
						"result_counts": map[string]int{
							"total": 1, "passed": 0, "failed": 1, "errored": 0, "skipped": 0,
						},
						"future_service_field": "preserved",
					})
					_, _ = w.Write(response)
				case strings.HasSuffix(r.URL.Path, "/output_items"):
					_, _ = io.WriteString(w, `{"data":[]}`)
				case strings.HasSuffix(r.URL.Path, "/run_new"):
					_, _ = w.Write(response)
				default:
					assert.NoError(t, json.NewEncoder(w).Encode(eval_api.OpenAIEvalRunList{
						Data: []eval_api.OpenAIEvalRun{*simulationReportingRun()},
					}))
				}
			}))
			t.Cleanup(srv.Close)
			var out bytes.Buffer
			command := &cobra.Command{}
			command.SetContext(t.Context())
			command.SetOut(&out)
			format := ""
			if tc.json {
				format = "json"
			}
			command.Flags().String("output", format, "")
			action := &runStartAction{cmd: command, flags: &runStartFlags{
				groupName: "eval_simulation", evalPath: t.TempDir(), wait: tc.wait,
			}}
			require.NoError(t, action.start(t.Context(), evalContextFor(srv), gate{}))
			assert.Equal(t, simulationMode, submitted.Metadata[metaRunMode])
			assert.Equal(t, "3", submitted.Metadata[metaSimulationRepetitions])
			assert.Equal(t, "8", submitted.Metadata[metaSimulationMaxTurns])
			assert.NotContains(t, submitted.Metadata, metaSimulationSeeds,
				"a reused file id was not downloaded, so its seed count is unknown")
			if tc.json {
				var result map[string]any
				require.NoError(t, json.Unmarshal(out.Bytes(), &result))
				if tc.wait {
					assert.Equal(t, "preserved", result["future_service_field"])
					assert.Contains(t, result, "metadata")
				} else {
					assert.Equal(t, "run_new", result["run_id"])
					assert.Equal(t, "eval_simulation", result["eval_id"])
					assert.NotContains(t, result, "metadata")
					assert.NotContains(t, result, "data_source")
				}
			} else {
				assert.Contains(t, out.String(), "CONVERSATION EVALUATION RESULTS")
				assert.Contains(t, out.String(), "Conversations completed  not reported")
				assert.Contains(t, out.String(), "Failed        1")
			}
		})
	}
}

func TestSimulationReportingDistinguishesAbsentNullAndZeroCounts(t *testing.T) {
	var run eval_api.OpenAIEvalRun
	require.NoError(t, json.Unmarshal([]byte(`{
		"id":"run_partial",
		"metadata":{"azd_run_mode":"conversation_simulation"},
		"result_counts":{"total":2,"passed":0,"failed":null,"errored":1}
	}`), &run))
	var out bytes.Buffer
	require.NoError(t, renderRun(&out, &run, nil))
	assert.Contains(t, out.String(), "Passed        0")
	assert.Contains(t, out.String(), "Failed     not reported")
	assert.Contains(t, out.String(), "Errored       1")
	assert.Contains(t, out.String(), "Skipped    not reported")
	assert.Contains(t, out.String(), "Pass rate  not reported")
}

func TestSimulationSeedCountComesFromValidatedDatasetRows(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/seeds.jsonl"):
			_, _ = io.WriteString(w, "{\"test_case_description\":\"first seed\"}\n"+
				"{\"test_case_description\":\"second seed\"}\n")
		case strings.HasSuffix(r.URL.Path, "/credentials"):
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]string{"sas_uri": srv.URL + "/seeds.jsonl"}))
		case strings.HasSuffix(r.URL.Path, "/versions"):
			_, _ = io.WriteString(w, `{"value":[{"name":"d","version":"1","id":"service-issued-id"}]}`)
		case strings.HasSuffix(r.URL.Path, "/versions/1"):
			_, _ = io.WriteString(w, `{"name":"d","version":"1","id":"service-issued-id"}`)
		default:
			t.Errorf("unexpected dataset request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	group := runnableSimulation()
	group.Simulation.NumConversations = 5
	source, _, err := evalContextFor(srv).simulationDataSource(t.Context(), group, "", 0)
	require.NoError(t, err)
	require.NotNil(t, source.SimulationSeedCount)
	assert.Equal(t, 2, *source.SimulationSeedCount)
	metadata := map[string]string{}
	recordSimulationMetadata(metadata, source)
	assert.Equal(t, "2", metadata[metaSimulationSeeds])
	assert.Equal(t, "5", metadata[metaSimulationRepetitions])
}
