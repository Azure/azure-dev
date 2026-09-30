// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilteredResultPageDoesNotReportPageSizeAsRunFailures(t *testing.T) {
	all := passingItems("pass1", "pass2", "pass3", "pass4", "pass5", "pass6")
	for i := range 12 {
		all = append(all, failingItem(fmt.Sprintf("fail%d", i+1)))
	}
	pages := map[string]*eval_api.OutputItemList{
		"":       {Data: all[:10], HasMore: true, LastID: "fail4"},
		"fail4":  {Data: all[10:]},
		"fail10": {Data: all[16:]},
	}
	run := &eval_api.OpenAIEvalRun{
		ID: "evalrun_counts", EvalID: "eval_counts", Status: "completed",
		ResultCounts: &eval_api.EvalRunResultCounts{Total: 18, Passed: 6, Failed: 12},
	}
	var summary bytes.Buffer
	require.NoError(t, renderRun(&summary, run, nil))
	assert.Contains(t, summary.String(), "Failed       12")

	for _, tc := range []struct {
		name  string
		after string
		shown int
	}{
		{"first page", "", 10},
		{"last page", "fail10", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page, err := filteredItemPage(t.Context(), nil, run.EvalID, run.ID, 10, tc.after,
				map[string]bool{itemFailed: true}, pagerFrom(pages))
			require.NoError(t, err)
			require.Len(t, page.Data, tc.shown)
			var out bytes.Buffer
			require.NoError(t, renderResults(&out, run.EvalID, run, page.Data, resultListView{failedOnly: true}))
			assert.Contains(t, out.String(), fmt.Sprintf("Showing %d failed test cases on this page.", tc.shown))
			assert.Contains(t, out.String(), "Full run: 12 failed of 18 total test cases (service-reported).")
			assert.NotContains(t, out.String(), fmt.Sprintf("%d of 18 test cases failed", tc.shown))
			assert.Equal(t, 12, run.ResultCounts.Failed)
		})
	}
}

func TestFilteredResultPageDoesNotLabelMovingCountsAsFullRun(t *testing.T) {
	run := &eval_api.OpenAIEvalRun{
		ID: "run_moving", EvalID: "eval_moving", Status: "in_progress",
		ResultCounts: &eval_api.EvalRunResultCounts{Total: 18, Passed: 6, Failed: 12},
	}
	var out bytes.Buffer
	require.NoError(t, renderResults(&out, run.EvalID, run,
		[]eval_api.OutputItem{failingItem("1")}, resultListView{failedOnly: true}))
	assert.Contains(t, out.String(), "Showing 1 failed test case on this page.")
	assert.NotContains(t, out.String(), "Full run:")
	assert.NotContains(t, out.String(), "Export complete results:")
	assert.Contains(t, out.String(), "Export available results:")
	assert.Contains(t, out.String(), "run output show 1 --eval eval_moving --run run_moving")

	run.Status = "completed"
	out.Reset()
	require.NoError(t, renderResults(&out, run.EvalID, run,
		[]eval_api.OutputItem{failingItem("1")}, resultListView{failedOnly: true}))
	assert.Contains(t, out.String(), "Full run: 12 failed of 18 total test cases")
	assert.Contains(t, out.String(), "Export complete results:")
}

func TestFilteredResultCountsDoNotInventMissingServiceTotals(t *testing.T) {
	for _, counts := range []*eval_api.EvalRunResultCounts{nil, {}} {
		run := &eval_api.OpenAIEvalRun{ID: "evalrun_counts", Status: "completed", ResultCounts: counts}
		var out bytes.Buffer
		require.NoError(t, renderResults(&out, "eval_counts", run,
			[]eval_api.OutputItem{failingItem("row")}, resultListView{failedOnly: true}))
		assert.Contains(t, out.String(), "Showing 1 failed test case on this page.")
		if counts == nil {
			assert.NotContains(t, out.String(), "Full run:")
		} else {
			assert.Contains(t, out.String(), "Full run: 0 failed of 0 total test cases (service-reported).")
		}
	}

}

func TestFilteredResultFooterRequiresReportedCounters(t *testing.T) {
	for _, tc := range []struct {
		name, counts string
		wantTotal    bool
	}{
		{"missing failed", `{"total":2}`, false},
		{"null failed", `{"total":2,"failed":null}`, false},
		{"missing total", `{"failed":1}`, false},
		{"null total", `{"total":null,"failed":1}`, false},
		{"no counters", `{}`, false},
		{"negative counter", `{"total":2,"failed":-1}`, false},
		{"explicit zeros", `{"total":0,"failed":0}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var run eval_api.OpenAIEvalRun
			require.NoError(t, json.Unmarshal([]byte(`{
					"id":"run_partial","status":"completed","metadata":{"azd_run_mode":"conversation_simulation"},
					"result_counts":`+tc.counts+`}`), &run))
			var out bytes.Buffer
			require.NoError(t, renderResults(&out, "eval_partial", &run,
				[]eval_api.OutputItem{failingItem("1")}, resultListView{failedOnly: true}))
			assert.Contains(t, out.String(), "Showing 1 failed test case on this page.")
			assert.Equal(t, tc.wantTotal, strings.Contains(out.String(), "Full run:"))
			if tc.wantTotal {
				assert.Contains(t, out.String(), "Full run: 0 failed of 0 total test cases (service-reported).")
			}
		})
	}
}

func TestOutputFiltersAgreeAcrossPagedBulkAndFileViews(t *testing.T) {
	for _, selection := range []struct {
		name, status string
		failedOnly   bool
		want         []string
	}{
		{"failed", "", true, []string{"row_failed"}},
		{"status failed", "failed", false, []string{"row_failed"}},
		{"errored", "errored", false, []string{"row_errored"}},
		{"combined", "errored", true, []string{"row_failed", "row_errored"}},
		{"no matches", "skipped", false, []string{}},
		{"unfiltered", "", false, []string{"row_passed", "row_failed", "row_errored"}},
	} {
		for _, view := range []string{"page", "all", "file"} {
			for _, format := range []string{"table", "json"} {
				t.Run(selection.name+"/"+view+"/"+format, func(t *testing.T) {
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						if strings.HasSuffix(r.URL.Path, "/output_items") {
							_, _ = w.Write([]byte(`{"data":[
								{"id":"row_passed","status":"passed","datasource_item":{"value":9007199254740993}},
								{"id":"row_failed","status":"failed","datasource_item":{"value":9007199254740993}},
								{"id":"row_errored","status":"errored","datasource_item":{"value":9007199254740993}}
							],"has_more":false}`))
						} else {
							assert.True(t, strings.HasSuffix(r.URL.Path, "/runs/run_filter"))
							_, _ = w.Write([]byte(`{"id":"run_filter","status":"completed",
								"result_counts":{"total":3,"passed":1,"failed":1,"errored":1,"skipped":0}}`))
						}
					}))
					t.Cleanup(srv.Close)
					var out bytes.Buffer
					command := jsonCmd(t, format)
					command.SetContext(t.Context())
					command.SetOut(&out)
					flags := &runOutputListFlags{
						status: selection.status, failedOnly: selection.failedOnly, all: view == "all",
					}
					if view == "file" {
						flags.outFile = filepath.Join(t.TempDir(), "filtered.json")
					}
					action := &runOutputListAction{cmd: command, flags: flags, runID: "run_filter"}
					require.NoError(t, action.list(t.Context(), evalContextFor(srv), "eval_filter"))
					body := out.Bytes()
					if view == "file" {
						var err error
						body, err = os.ReadFile(flags.outFile)
						require.NoError(t, err)
					}
					if format == "json" || view == "file" {
						var items []struct{ ID string }
						if view == "file" {
							require.NoError(t, json.Unmarshal(body, &items))
						} else {
							var page struct {
								Items []struct{ ID string } `json:"items"`
								Count int                   `json:"count"`
							}
							require.NoError(t, json.Unmarshal(body, &page))
							assert.Equal(t, len(selection.want), page.Count)
							items = page.Items
						}
						got := make([]string, 0, len(items))
						for _, item := range items {
							got = append(got, item.ID)
						}
						assert.Equal(t, selection.want, got)
						if len(items) > 0 {
							assert.Contains(t, string(body), "9007199254740993")
						}
					} else {
						for _, id := range []string{"row_passed", "row_failed", "row_errored"} {
							assert.Equal(t, strings.Contains(strings.Join(selection.want, ","), id),
								strings.Contains(string(body), id), id)
						}
						if selection.name == "failed" || selection.name == "status failed" {
							if view == "all" {
								assert.Contains(t, string(body), "Showing 1 failed test case.")
								assert.NotContains(t, string(body), "on this page")
							} else {
								assert.Contains(t, string(body), "Showing 1 failed test case on this page.")
							}
							assert.Contains(t, string(body), "Full run: 1 failed of 3 total test cases")
						}
						if selection.name == "no matches" {
							assert.Contains(t, string(body), "No results match the selected status filter.")
							assert.NotContains(t, string(body), "No rows have been scored yet.")
						}
					}
				})
			}
		}

	}
}

func TestUnfilteredEmptyOutputDoesNotClaimAFilterExcludedRows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/output_items") {
			_, _ = io.WriteString(w, `{"data":[]}`)
		} else {
			_, _ = io.WriteString(w, `{"id":"run_empty","status":"completed","result_counts":{"total":0}}`)
		}
	}))
	t.Cleanup(srv.Close)
	for _, all := range []bool{false, true} {
		command := jsonCmd(t, "table")
		command.SetContext(t.Context())
		var out bytes.Buffer
		command.SetOut(&out)
		action := &runOutputListAction{cmd: command, runID: "run_empty", flags: &runOutputListFlags{all: all}}
		require.NoError(t, action.list(t.Context(), evalContextFor(srv), "eval_empty"))
		assert.Contains(t, out.String(), "No rows have been scored yet.")
		assert.NotContains(t, out.String(), "selected status filter")
	}
}
