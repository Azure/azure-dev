// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOutputCallersRetainResolvedRunIDWhenServiceOmitsIt(t *testing.T) {
	const runResponse = `{"status":"completed","result_counts":{"total":1,"passed":null},
		"metadata":{"azd_eval":"mutable_name"},"unknown":9007199254740993}`
	const itemResponse = `{"id":"item_1","run_id":"run_resolved","status":"completed",
		"datasource_item":{"value":9007199254740993},
		"results":[{"name":"quality","score":1,"passed":true}]}`
	const runPath = "/openai/v1/evals/eval_resolved/runs/run_resolved"
	for _, identity := range []string{"explicit", "remembered", "latest"} {
		for _, operation := range []string{"show", "list", "export", "run show"} {
			for _, format := range []string{"json", "table"} {
				t.Run(identity+"/"+operation+"/"+format, func(t *testing.T) {
					var paths []string
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						assert.Equal(t, http.MethodGet, r.Method)
						paths = append(paths, r.URL.Path)
						w.Header().Set("Content-Type", "application/json")
						switch r.URL.Path {
						case "/openai/v1/evals/eval_resolved/runs":
							assert.Equal(t, "latest", identity)
							_, _ = w.Write([]byte(`{"data":[{"id":"run_resolved","status":"completed"}]}`))
						case runPath:
							_, _ = w.Write([]byte(runResponse))
						case runPath + "/output_items":
							_, _ = w.Write([]byte(`{"data":[` + itemResponse + `],"has_more":false}`))
						case runPath + "/output_items/item_1":
							_, _ = w.Write([]byte(itemResponse))
						default:
							t.Errorf("unexpected or unresolved endpoint: %s", r.URL.Path)
							w.WriteHeader(http.StatusNotFound)
						}
					}))
					t.Cleanup(srv.Close)
					ec := evalContextFor(srv)
					requestedID := ""
					switch identity {
					case "explicit":
						requestedID = "run_resolved"
					case "remembered":
						ec.state = map[string]string{idKey("evalrun", "eval_resolved"): "run_resolved"}
					}
					var out, stderr bytes.Buffer
					command := jsonCmd(t, format)
					command.SetContext(t.Context())
					command.SetOut(&out)
					command.SetErr(&stderr)
					switch operation {
					case "show":
						action := &runOutputShowAction{
							cmd: command, itemID: "item_1", flags: &runOutputShowFlags{run: requestedID},
						}
						require.NoError(t, action.showRun(t.Context(), ec, "eval_resolved"))
						assert.Contains(t, paths, runPath+"/output_items/item_1")
					case "list":
						action := &runOutputListAction{cmd: command, runID: requestedID, flags: &runOutputListFlags{}}
						require.NoError(t, action.list(t.Context(), ec, "eval_resolved"))
						assert.Contains(t, paths, runPath+"/output_items")
						if format == "table" {
							assert.Contains(t, out.String(), "--eval eval_resolved --run run_resolved")
						}
					case "export":
						action := &runOutputExportAction{cmd: command, runID: requestedID, flags: &runOutputExportFlags{}}
						require.NoError(t, action.export(t.Context(), ec, "eval_resolved", exportToStdout))
						assert.Contains(t, paths, runPath+"/output_items")
						assert.Contains(t, paths, runPath)
						var document exportDocument
						require.NoError(t, json.Unmarshal(out.Bytes(), &document))
						var compact bytes.Buffer
						require.NoError(t, json.Compact(&compact, []byte(runResponse)))
						var actual bytes.Buffer
						require.NoError(t, json.Compact(&actual, document.Run))
						assert.Equal(t, compact.String(), actual.String(), "raw export keeps absent ID and exact values")
					case "run show":
						action := &runShowAction{cmd: command, runID: requestedID, flags: &runShowFlags{}}
						require.NoError(t, action.show(t.Context(), ec, "eval_resolved", gate{}))
						if format == "json" && identity != "latest" {
							var document map[string]json.RawMessage
							require.NoError(t, json.Unmarshal(out.Bytes(), &document))
							assert.Empty(t, string(document["id"]), "routing must not add an ID to raw service JSON")
							assert.Equal(t, "9007199254740993", string(document["unknown"]))
						}
					}
					for _, path := range paths {
						assert.NotContains(t, path, "/runs//")
					}
					if identity == "remembered" && format == "table" {
						assert.Contains(t, stderr.String(), "Using last run: run_resolved")
					}
					if identity != "latest" {
						assert.NotContains(t, paths, "/openai/v1/evals/eval_resolved/runs",
							"never pick a different run when an explicit or remembered lookup succeeded")
					}
				})
			}
		}
	}
}

func TestResolvedLookupIDDoesNotRewriteServiceRun(t *testing.T) {
	for _, returnedID := range []string{``, `,"id":""`, `,"id":null`, `,"id":"service_alias"`} {
		t.Run(returnedID, func(t *testing.T) {
			response := `{"status":"completed"` + returnedID + `}`
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.True(t, strings.HasSuffix(r.URL.Path, "/run_lookup"))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(response))
			}))
			t.Cleanup(srv.Close)
			command := jsonCmd(t, "json")
			command.SetContext(t.Context())
			run, id, err := evalContextFor(srv).latestOrNamedRun(command, "eval_1", "run_lookup", true)
			require.NoError(t, err)
			assert.Equal(t, "run_lookup", id)
			expected := ""
			if strings.Contains(returnedID, "service_alias") {
				expected = "service_alias"
			}
			assert.Equal(t, expected, run.ID, "lookup identity remains separate from the model")
			encoded, err := json.Marshal(run)
			require.NoError(t, err)
			assert.JSONEq(t, response, string(encoded), "absent, null and service IDs remain as returned")
		})
	}
}

func TestRunLookupRefusesAnUnidentifiedNewestRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.True(t, strings.HasSuffix(r.URL.Path, "/runs"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"status":"completed"}]}`))
	}))
	t.Cleanup(srv.Close)
	command := jsonCmd(t, "json")
	command.SetContext(t.Context())
	run, id, err := evalContextFor(srv).latestOrNamedRun(command, "eval_1", "", true)
	require.Error(t, err)
	assert.Nil(t, run)
	assert.Empty(t, id)
	assert.Contains(t, err.Error(), `service omitted the newest run ID for eval "eval_1"`)
	assert.Contains(t, err.Error(), "supply --run with a known run ID")
	assert.NotContains(t, err.Error(), "changes a run")
}

func TestLegacyOutputCountsDoNotClaimKnownCompletion(t *testing.T) {
	var run eval_api.OpenAIEvalRun
	require.NoError(t, json.Unmarshal([]byte(`{"id":"run_legacy","result_counts":{"total":1,"failed":1}}`), &run))
	var out bytes.Buffer
	require.NoError(t, renderResults(&out, "eval_legacy", &run, []eval_api.OutputItem{failingItem("1")}, true))
	assert.Contains(t, out.String(), "Showing 1 failed test case")
	assert.Contains(t, out.String(), "Export available results:")
	assert.NotContains(t, out.String(), "Export complete results:")
	assert.NotContains(t, out.String(), "Full run:")
}
