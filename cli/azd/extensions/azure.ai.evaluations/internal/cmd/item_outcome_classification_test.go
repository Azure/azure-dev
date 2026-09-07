// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
)

// The RESULTS cell is read without a legend.
//
// It used to print `2P/1F/1U`, which needed one, and whose U stood for two
// different things at once: an evaluator the service skipped and an evaluator
// that failed to run were the same letter.
func TestResultsBreakdownSpellsEveryCountOut(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   itemOutcome
		want string
	}{
		{"all passed", itemOutcome{Passed: 4}, "4 passed"},
		{"mixed", itemOutcome{Passed: 3, Failed: 1}, "3 passed, 1 failed"},
		{"skipped only", itemOutcome{Skipped: 1}, "1 skipped"},
		{"errors plural", itemOutcome{Errored: 4}, "4 errors"},
		{"error singular", itemOutcome{Errored: 1}, "1 error"},
		{"skip and error apart", itemOutcome{Passed: 2, Skipped: 1, Errored: 1},
			"2 passed, 1 skipped, 1 error"},
		{"no results", itemOutcome{}, "-"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.in.ResultsBreakdown())
			assert.NotContains(t, tc.in.ResultsBreakdown(), "/",
				"the abbreviated form is what needed the legend")
		})
	}
}

// A canonical status from the service is the answer; a lifecycle one is not.
//
// The service reports `completed` on every item of a run whose samples all
// errored, with the failure visible only on each result. Showing that verbatim
// told the reader the row was fine.
func TestItemStatusKeepsWhatTheServiceSaidAndDerivesTheRest(t *testing.T) {
	assert.Equal(t, itemSkipped, itemStatusFor("skipped", itemOutcome{Failed: 1}),
		"the service named an outcome, so it is not second-guessed")
	assert.Equal(t, itemErrored, itemStatusFor("  ERRORED ", itemOutcome{Passed: 1}),
		"spelled however it arrived")

	assert.Equal(t, itemFailed, itemStatusFor("completed", itemOutcome{Passed: 3, Failed: 1, Errored: 1}),
		"one failing evaluator decides the row")
	assert.Equal(t, itemErrored, itemStatusFor("completed", itemOutcome{Passed: 3, Errored: 1}),
		"then one that never ran")
	assert.Equal(t, itemPassed, itemStatusFor("completed", itemOutcome{Passed: 3, Skipped: 1}),
		"a skip alongside passes does not unmake them")
	assert.Equal(t, itemSkipped, itemStatusFor("completed", itemOutcome{Skipped: 2}),
		"and nothing but skips is a skip, not a failure to run")
}

// The row's counts have to reconcile against what the evaluators returned, and
// the ATTENTION column has to name which of them is why.
func TestClassifyItemSeparatesSkipsFromErrors(t *testing.T) {
	got := classifyItem(eval_api.OutputItem{
		ID:     "oi_1",
		Status: "completed",
		Results: []eval_api.OutputResult{
			{Name: "coherence", Passed: new(true), Reason: "Read well."},
			{Name: "relevance", Passed: new(false), Reason: "Answered a different question."},
			{Name: "fluency", Status: "skipped"},
			{Name: "groundedness", Sample: &eval_api.OutputSample{
				Error: &eval_api.SampleError{Code: "FAILED_EXECUTION"}}},
		},
	})

	assert.Equal(t, itemFailed, got.Status)
	assert.Equal(t, 4, got.Total())
	assert.Equal(t, "1 passed, 1 failed, 1 skipped, 1 error", got.ResultsBreakdown())
	assert.Equal(t,
		[]string{"relevance: failed", "fluency: skipped", "groundedness: errored"},
		got.Attention,
		"a passing evaluator is not something to look at")
	assert.Equal(t, "Answered a different question.", got.Reason,
		"the failure is what the reader came for, whichever result arrived first")
}
