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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHumanRunViewsPreservePartialCountPresence(t *testing.T) {
	for _, render := range []struct {
		name string
		call func(io.Writer, *eval_api.OpenAIEvalRun) error
	}{
		{"summary", func(out io.Writer, run *eval_api.OpenAIEvalRun) error { return renderRun(out, run, nil) }},
		{"detail", renderRunDetail},
		{"output", func(out io.Writer, run *eval_api.OpenAIEvalRun) error {
			return renderResults(out, "eval_partial", run, nil, false)
		}},
	} {
		for _, counts := range []string{
			`{"total":2}`,
			`{"total":2,"passed":null,"failed":null,"errored":null,"skipped":null}`,
			`{"passed":2}`,
			`{}`,
		} {
			t.Run(render.name+"/"+counts, func(t *testing.T) {
				var run eval_api.OpenAIEvalRun
				require.NoError(t, json.Unmarshal([]byte(`{
					"id":"run_partial","eval_id":"eval_partial","status":"completed","result_counts":`+counts+`}`), &run))
				before, err := json.Marshal(run)
				require.NoError(t, err)
				var out bytes.Buffer
				require.NoError(t, render.call(&out, &run))
				text := out.String()
				assert.Contains(t, strings.ToLower(text), "not reported")
				for _, invented := range []string{
					"0 failed", "0 errored", "Errored       2", "Errored       0", "Failed        0",
				} {
					assert.NotContains(t, text, invented)
				}
				after, err := json.Marshal(run)
				require.NoError(t, err)
				assert.Equal(t, string(before), string(after))
			})
		}
	}
}

func TestHumanRunViewsKeepExplicitZeroCounts(t *testing.T) {
	var run eval_api.OpenAIEvalRun
	require.NoError(t, json.Unmarshal([]byte(`{"id":"run_zero","status":"completed",
		"result_counts":{"total":0,"passed":0,"failed":0,"errored":0,"skipped":0}}`), &run))
	var out bytes.Buffer
	require.NoError(t, renderRunDetail(&out, &run))
	assert.Contains(t, out.String(), "0 passed, 0 failed, 0 errored")
	out.Reset()
	require.NoError(t, renderResults(&out, "eval_zero", &run, nil, false))
	assert.Contains(t, out.String(), "0 test cases: 0 passed, 0 failed, 0 errored, 0 skipped")
}

func TestRunCallersRenderMissingCountMembersAsUnreported(t *testing.T) {
	const response = `{"id":"run_partial","status":"completed","result_counts":{"total":2},
		"unknown":{"value":9007199254740993}}`
	for _, caller := range []string{"start", "show", "output list"} {
		for _, format := range []string{"table", "json"} {
			t.Run(caller+"/"+format, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch {
					case strings.HasSuffix(r.URL.Path, "/output_items"):
						_, _ = io.WriteString(w, `{"data":[]}`)
					case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/runs"):
						_, _ = io.WriteString(w, `{"id":"run_partial","status":"queued"}`)
					case strings.HasSuffix(r.URL.Path, "/runs/run_partial"):
						_, _ = io.WriteString(w, response)
					case strings.HasSuffix(r.URL.Path, "/runs"):
						_, _ = io.WriteString(w, `{"data":[{"id":"previous","data_source":{"type":"jsonl"}}]}`)
					default:
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
						w.WriteHeader(http.StatusNotFound)
					}
				}))
				t.Cleanup(srv.Close)
				var out bytes.Buffer
				command := jsonCmd(t, format)
				command.SetContext(t.Context())
				command.SetOut(&out)
				ec := evalContextFor(srv)
				switch caller {
				case "start":
					action := &runStartAction{cmd: command, flags: &runStartFlags{
						groupName: "eval_partial", evalPath: t.TempDir(), wait: true,
					}}
					require.NoError(t, action.start(t.Context(), ec, gate{}))
				case "show":
					action := &runShowAction{cmd: command, runID: "run_partial", flags: &runShowFlags{}}
					require.NoError(t, action.show(t.Context(), ec, "eval_partial", gate{}))
				case "output list":
					action := &runOutputListAction{cmd: command, runID: "run_partial", flags: &runOutputListFlags{}}
					require.NoError(t, action.list(t.Context(), ec, "eval_partial"))
				}
				if format == "json" {
					if caller != "output list" {
						var document map[string]json.RawMessage
						require.NoError(t, json.Unmarshal(out.Bytes(), &document))
						assert.JSONEq(t, `{"total":2}`, string(document["result_counts"]))
						assert.Contains(t, string(document["unknown"]), "9007199254740993")
					}
					return
				}
				text := out.String()
				assert.Contains(t, text, "not reported")
				for _, invented := range []string{"0 failed", "0 errored", "Errored       2", "Errored       0"} {
					assert.NotContains(t, text, invented)
				}
				assert.NotContains(t, text, "--status errored")
			})
		}
	}
}

func TestRunSummaryDoesNotInferErroredRowsOverExplicitZero(t *testing.T) {
	for _, status := range []string{"in_progress", "completed", ""} {
		run := &eval_api.OpenAIEvalRun{
			ID: "run_counts", EvalID: "eval_counts", Status: status,
			ResultCounts: &eval_api.EvalRunResultCounts{Total: 2},
		}
		var out bytes.Buffer
		require.NoError(t, renderRun(&out, run, nil))
		assert.Contains(t, out.String(), "Errored       0")
		assert.NotContains(t, out.String(), "Errored       2")
		assert.NotContains(t, out.String(), "--status errored")
	}
}

func TestRunListFieldsUseReportedCounts(t *testing.T) {
	var run eval_api.OpenAIEvalRun
	require.NoError(t, json.Unmarshal([]byte(`{"id":"run_partial","result_counts":{"passed":2}}`), &run))
	assert.Equal(t, "not reported", reportedSampleCount(&run))
	assert.Equal(t, "not reported", reportedRunPassRate(&run))
	require.NoError(t, json.Unmarshal([]byte(`{
		"id":"run_partial","result_counts":{"total":2,"passed":2,"failed":0}}`), &run))
	assert.Equal(t, "2", reportedSampleCount(&run))
	assert.Equal(t, "100.0%", reportedRunPassRate(&run))
}

func TestMovingGateUsesResolvedIDWithoutChangingJSON(t *testing.T) {
	const response = `{"status":"in_progress","result_counts":{"total":2}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.True(t, strings.HasSuffix(r.URL.Path, "/runs/run_resolved"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(srv.Close)
	for _, format := range []string{"table", "json"} {
		command := jsonCmd(t, format)
		command.SetContext(t.Context())
		var out bytes.Buffer
		command.SetOut(&out)
		action := &runShowAction{cmd: command, runID: "run_resolved", flags: &runShowFlags{}}
		err := action.show(t.Context(), evalContextFor(srv), "eval_resolved", gate{set: true})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "run_resolved")
		assert.Contains(t, err.Error(), "in_progress")
		assert.Empty(t, out.String())
	}
}
