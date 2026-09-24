// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
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
			require.NoError(t, renderResults(&out, run.EvalID, run, page.Data, true))
			assert.Contains(t, out.String(), fmt.Sprintf("Showing %d failed test cases on this page.", tc.shown))
			assert.Contains(t, out.String(), "Full run: 12 failed of 18 total test cases (service-reported).")
			assert.NotContains(t, out.String(), fmt.Sprintf("%d of 18 test cases failed", tc.shown))
			assert.Equal(t, 12, run.ResultCounts.Failed)
		})
	}
}

func TestFilteredResultCountsDoNotInventMissingServiceTotals(t *testing.T) {
	for _, counts := range []*eval_api.EvalRunResultCounts{nil, {}} {
		run := &eval_api.OpenAIEvalRun{ID: "evalrun_counts", ResultCounts: counts}
		var out bytes.Buffer
		require.NoError(t, renderResults(&out, "eval_counts", run, []eval_api.OutputItem{failingItem("row")}, true))
		assert.Contains(t, out.String(), "Showing 1 failed test cases on this page.")
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
					"id":"run_partial","metadata":{"azd_run_mode":"conversation_simulation"},
					"result_counts":`+tc.counts+`}`), &run))
			var out bytes.Buffer
			require.NoError(t, renderResults(&out, "eval_partial", &run, []eval_api.OutputItem{failingItem("1")}, true))
			assert.Contains(t, out.String(), "Showing 1 failed test cases on this page.")
			assert.Equal(t, tc.wantTotal, strings.Contains(out.String(), "Full run:"))
			if tc.wantTotal {
				assert.Contains(t, out.String(), "Full run: 0 failed of 0 total test cases (service-reported).")
			}
		})
	}
}
