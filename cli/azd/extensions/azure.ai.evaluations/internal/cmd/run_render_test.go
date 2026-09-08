// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scoredRun is a run the way the service returns one, with rows attached.
func scoredRows() []eval_api.OutputItem {
	return []eval_api.OutputItem{
		{
			ID: "oi_1",
			Results: []eval_api.OutputResult{
				{Name: "relevance", Passed: new(true), Score: 5},
				{Name: "coherence", Passed: new(true), Score: 4},
			},
		},
		{
			ID: "oi_2",
			Results: []eval_api.OutputResult{
				{Name: "relevance", Passed: new(false), Score: 1, Reason: "Answered a different question."},
				{Name: "coherence", Passed: new(false), Score: 2, Reason: "Rambled."},
			},
		},
	}
}

// One evaluated sample is one row. Listing a sample once per evaluator makes a
// run with three evaluators look three times as broken as it is, and
// --failed-only exists to answer "which samples do I go and look at".
func TestRenderResultsIsOneRowPerSample(t *testing.T) {
	var out bytes.Buffer
	run := &eval_api.OpenAIEvalRun{ID: "evalrun_1", Status: "completed"}
	require.NoError(t, renderResults(&out, "an-eval", run, scoredRows(), false))

	text := out.String()
	assert.Equal(t, 1, strings.Count(text, "oi_2"),
		"a sample that failed two evaluators must still be one row:\n%s", text)

	for _, header := range []string{"ITEM", "STATUS", "RESULTS"} {
		assert.Containsf(t, text, header, "the listing lost its %s column", header)
	}
	// RESULTS spec 5.2 and 9.1 fix the table at three columns, and 5.2 puts
	// evaluator names and reasons in `output show`. Both were columns here, and
	// both were truncated to fit.
	for _, gone := range []string{"ATTENTION", "REASON"} {
		assert.NotContainsf(t, text, gone,
			"%s belongs to `output show`, where it is not cut to a cell", gone)
	}

	// The old SAMPLE column numbered within the current filter, so the same
	// sample carried a different number depending on the flags while reading
	// like an identifier. ITEM carries the id `run output show` accepts.
	assert.NotContains(t, text, "SAMPLE",
		"a position that changes with the filter must not sit beside the id")
	assert.NotContains(t, text, "FAILED EVALUATORS",
		"the column also carries evaluators that returned no verdict, which did not fail")
	// Evaluators measure different things on different scales, so a single
	// number across them is one no evaluator reported. Scores stay per
	// evaluator, in `run output show`.
	assert.NotContains(t, text, "SCORE",
		"an item-level aggregate score names an aggregation the product has not defined")
}

// The failing row states its outcome and the arithmetic behind it. Which
// evaluator failed is `output show`'s answer, per RESULTS spec 5.2.
func TestRenderResultsNamesEveryFailedEvaluator(t *testing.T) {
	var out bytes.Buffer
	run := &eval_api.OpenAIEvalRun{ID: "evalrun_1", Status: "completed"}
	require.NoError(t, renderResults(&out, "an-eval", run, scoredRows(), true))

	text := out.String()
	assert.Contains(t, text, "2 failed",
		"the row counts the evaluator results behind its status")
	assert.NotContains(t, text, "Answered a different question.",
		"the reason is one evaluator's account of the row, and lives in `output show`")
	assert.NotContains(t, text, "oi_1", "--failed-only must drop the passing sample")
	assert.Contains(t, text, "1 of 2 test cases failed",
		"the status reads as the verb, which is what a reader says out loud")
}

// --failed-only means failed.
//
// It used to keep any row that did not pass, so a run that errored everywhere
// answered it with rows the totals counted as errored, and the footer then
// called them failures. An unscored row is reachable, but by asking for it.
func TestFailedOnlyExcludesRowsNothingScored(t *testing.T) {
	items := []eval_api.OutputItem{
		{ID: "oi_fail", Results: []eval_api.OutputResult{
			{Name: "relevance", Passed: new(false), Score: 1, Reason: "Answered a different question."},
		}},
		// No verdict: the evaluator errored on this row rather than scoring it.
		{ID: "oi_unscored", Results: []eval_api.OutputResult{{Name: "relevance"}}},
	}

	var out bytes.Buffer
	run := &eval_api.OpenAIEvalRun{ID: "evalrun_1", Status: "completed"}
	require.NoError(t, renderResults(&out, "an-eval", run, filterItems(items, map[string]bool{itemFailed: true}), true))

	text := out.String()
	assert.Contains(t, text, "oi_fail")
	assert.NotContains(t, text, "oi_unscored",
		"a row nothing scored did not fail:\n%s", text)
	assert.NotContains(t, text, "2 sample(s) failed",
		"an unscored row is not a failing one")
}

// The row itself has to say which evaluator returned nothing, and the item has
// to be reachable by the outcome it actually has.
func TestErroredRowIsReportedAsErrored(t *testing.T) {
	items := []eval_api.OutputItem{
		{ID: "oi_unscored", Results: []eval_api.OutputResult{{Name: "relevance"}}},
	}

	var out bytes.Buffer
	run := &eval_api.OpenAIEvalRun{ID: "evalrun_1", Status: "completed"}
	require.NoError(t, renderResults(&out, "an-eval", run, items, false))

	text := out.String()
	assert.Contains(t, text, itemErrored, "the status column states the outcome directly")
	assert.Contains(t, text, "1 error",
		"and the row counts what stands behind that status")
	assert.NotContains(t, text, "no verdict",
		"which is stated as the outcome it is, not as an absence the reader decodes")
}

// The listing is paged and every cell is truncated, so the two commands that
// get past both have to be printed with it -- resolved, not as a shape.
//
// A reader who has to work out which eval a run belonged to, and then retype a
// row id, is being asked to redo the lookup the listing just did.
func TestListingPrintsItsFollowUpCommandsResolved(t *testing.T) {
	run := &eval_api.OpenAIEvalRun{
		ID:       "evalrun_1",
		Status:   "completed",
		Metadata: map[string]string{metaEvalName: "support-agent-dataset-eval"},
	}
	items := []eval_api.OutputItem{{
		ID:      "oi_first",
		Results: []eval_api.OutputResult{{Name: "relevance", Passed: new(false), Reason: "Off topic."}},
	}}

	var out bytes.Buffer
	require.NoError(t, renderResults(&out, "an-eval", run, items, false))
	text := out.String()

	assert.Contains(t, text,
		"azd ai eval run output show oi_first --eval support-agent-dataset-eval --run evalrun_1",
		"the detail command names a row from the table above it:\n%s", text)
	assert.Contains(t, text, "azd ai eval run output export --eval support-agent-dataset-eval --run evalrun_1")
	assert.NotContains(t, text, "<item>",
		"a line with a placeholder in it reads like a command and is not one")
}

// filterItems mirrors what the command does, so the render tests exercise the
// same predicate the listing uses.
func filterItems(items []eval_api.OutputItem, keep map[string]bool) []eval_api.OutputItem {
	kept := make([]eval_api.OutputItem, 0, len(items))
	for _, it := range items {
		if keep[classifyItem(it).Status] {
			kept = append(kept, it)
		}
	}
	return kept
}

// The run summary carries pass and fail counts but no score, so the mean has
// to be averaged over the rows an evaluator actually scored.
func TestCriteriaMeans(t *testing.T) {
	means := criteriaMeans(scoredRows())
	assert.InDelta(t, 3.0, means["relevance"], 0.001)
	assert.InDelta(t, 3.0, means["coherence"], 0.001)

	assert.Nil(t, criteriaMeans(nil), "no rows means no column, not a column of zeroes")
}

// An unscored row is not a zero. Counting it as one drags the average toward a
// number no evaluator produced.
func TestCriteriaMeansIgnoresUnscoredRows(t *testing.T) {
	rows := []eval_api.OutputItem{
		{Results: []eval_api.OutputResult{{Name: "relevance", Score: 4, Passed: new(true)}}},
		{Results: []eval_api.OutputResult{{Name: "relevance"}}},
	}
	// The zero value of a score is undefined, not 0.0.
	rows[1].Results[0].Score = eval_api.LenientFloat(0)

	means := criteriaMeans(rows)
	require.Contains(t, means, "relevance")
	assert.InDelta(t, 2.0, means["relevance"], 0.001,
		"a defined zero counts; this pins the arithmetic so the undefined case is visible")
}

// The header the spec documents, and the identity a person needs to know which
// run they are looking at.
func TestRenderRunHeaderNamesTheEval(t *testing.T) {
	run := &eval_api.OpenAIEvalRun{
		ID:           "evalrun_9",
		EvalID:       "eval_9",
		Status:       "completed",
		Metadata:     map[string]string{"azd_eval": "support-agent-smoke"},
		ResultCounts: &eval_api.EvalRunResultCounts{Total: 15, Passed: 12, Failed: 3},
		CreatedAt:    float64(1785801525),
		ModifiedAt:   float64(1785802119),
	}

	var out bytes.Buffer
	require.NoError(t, renderRun(&out, run, map[string]float64{"relevance": 4.1}))
	text := out.String()

	assert.Contains(t, text, "Run        evalrun_9")
	assert.Contains(t, text, "Eval       support-agent-smoke",
		"the declared name is what the author recognizes, not the service id")
	assert.Contains(t, text, "Status     completed")
	assert.Contains(t, text, "Total        15",
		"the sample count is stated once, in the block that also breaks it down")
	assert.Contains(t, text, "Duration   9m54s")
}

// Without the metadata the extension writes at create time there is no
// declared name, so the id is the honest answer rather than a blank.
func TestRenderRunHeaderFallsBackToTheEvalID(t *testing.T) {
	var out bytes.Buffer
	run := &eval_api.OpenAIEvalRun{ID: "evalrun_9", EvalID: "eval_9", Status: "queued"}
	require.NoError(t, renderRun(&out, run, nil))
	assert.Contains(t, out.String(), "Eval       eval_9")
}

// The score column is dropped rather than filled with dashes when the rows
// were never read, so the table does not imply the run produced no scores.
func TestRenderRunOmitsTheScoreColumnWithoutMeans(t *testing.T) {
	run := &eval_api.OpenAIEvalRun{
		ID: "evalrun_9", Status: "completed",
		PerTestingCriteria: []eval_api.EvalRunCriteriaResult{{TestingCriteria: "relevance", Passed: 2}},
	}

	var without bytes.Buffer
	require.NoError(t, renderRun(&without, run, nil))
	assert.NotContains(t, without.String(), "MEAN SCORE")

	var with bytes.Buffer
	require.NoError(t, renderRun(&with, run, map[string]float64{"relevance": 4.15}))
	assert.Contains(t, with.String(), "MEAN SCORE")
	assert.Contains(t, with.String(), "4.2", "the mean is shown to one decimal")
}

// `run output show` is what a person opens after a failing listing, so it has
// to be readable. It used to emit raw JSON whatever was asked for.
func TestRenderOutputItemIsNotJSON(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, renderOutputItem(&out, &eval_api.OutputItem{
		ID:     "oi_01JQZY7K3R",
		RunID:  "evalrun_1",
		Status: "fail",
		Results: []eval_api.OutputResult{{
			Name:   "builtin.task_adherence",
			Score:  0.35,
			Passed: new(false),
			Reason: "Task abandoned after the first clarifying question.",
		}},
	}))

	text := out.String()
	assert.NotContains(t, text, `"results"`, "the detail view must not be JSON")
	for _, want := range []string{
		"Item", "oi_01JQZY7K3R",
		"Status", "fail",
		"builtin.task_adherence", "0.35",
		"Task abandoned after the first clarifying question.",
	} {
		assert.Contains(t, text, want)
	}
}

// The listing truncates the reason to a cell, so this view exists to carry the
// whole of it. Cutting it here would leave it readable nowhere.
func TestRenderOutputItemKeepsTheWholeReason(t *testing.T) {
	reason := strings.Repeat("a reason that runs well past any column width. ", 8)

	var out bytes.Buffer
	require.NoError(t, renderOutputItem(&out, &eval_api.OutputItem{
		ID:      "oi_1",
		Status:  "fail",
		Results: []eval_api.OutputResult{{Name: "relevance", Passed: new(false), Reason: reason}},
	}))

	assert.Contains(t, out.String(), reason)
}

// A rubric reports one result per dimension, all carrying the evaluator's
// name. Printed flat they read as several evaluators that share a name.
func TestRenderOutputItemGroupsARubricsDimensions(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, renderOutputItem(&out, &eval_api.OutputItem{
		ID:     "oi_1",
		Status: "fail",
		Results: []eval_api.OutputResult{
			{Name: "support-agent-quality", Metric: "resolves_issue", Score: 1, Passed: new(false)},
			{Name: "support-agent-quality", Metric: "cites_policy", Score: 5, Passed: new(true)},
			{Name: "builtin.task_adherence", Score: 0.35, Passed: new(false)},
		},
	}))

	text := out.String()
	assert.Equal(t, 1, strings.Count(text, "support-agent-quality"),
		"the evaluator is named once, above its dimensions:\n%s", text)
	for _, want := range []string{"resolves_issue", "cites_policy", "builtin.task_adherence"} {
		assert.Contains(t, text, want)
	}
}

// The service echoes the evaluator's name in `metric` for a single-score
// evaluator. Nesting that reads as a dimension that happens to share its
// evaluator's name.
func TestRenderOutputItemDoesNotNestASelfNamedMetric(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, renderOutputItem(&out, &eval_api.OutputItem{
		ID:     "oi_1",
		Status: "completed",
		Results: []eval_api.OutputResult{
			{Name: "task_adherence", Metric: "task_adherence", Score: 1, Passed: new(true)},
		},
	}))

	assert.Equal(t, 1, strings.Count(out.String(), "task_adherence"),
		"the evaluator must be named once:\n%s", out.String())
}

// The criterion table has to account for every sample the run had.
//
// It reported only PASSED and FAILED, so a 15-item run whose evaluator skipped
// one showed 12 and 2 and left the reader to find the fifteenth by scanning
// every row or reading the JSON. The service models four statuses; all four are
// columns now, and SCORED names the denominator the rate is taken over.
func TestCriterionTableAccountsForEverySample(t *testing.T) {
	run := &eval_api.OpenAIEvalRun{
		ID:     "evalrun_1",
		Status: "completed",
		ResultCounts: &eval_api.EvalRunResultCounts{
			Total: 15, Passed: 9, Failed: 6, Errored: 0, Skipped: 0,
		},
		PerTestingCriteria: []eval_api.EvalRunCriteriaResult{
			{TestingCriteria: "task_adherence", Passed: 12, Failed: 2, Errored: 0, Skipped: 1},
			{TestingCriteria: "custom_rubric", Passed: 11, Failed: 4, Errored: 0, Skipped: 0},
		},
	}

	var out bytes.Buffer
	require.NoError(t, renderResults(&out, "an-eval", run, nil, false))
	text := out.String()

	for _, header := range []string{"PASS", "FAIL", "SKIP", "ERROR", "SCORED", "PASS RATE"} {
		assert.Containsf(t, text, header, "the criterion table lost its %s column:\n%s", header, text)
	}
	assert.Contains(t, text, "14/15",
		"SCORED states how much of the run the evaluator actually judged")
	assert.Contains(t, text, "85.7%",
		"the rate is taken over what was scored, not over every sample")

	// The two tables are read against each other, so the arithmetic between
	// them has to be on screen rather than inferred.
	assert.Contains(t, text, "15 test cases x 2 evaluators = 30 evaluator results")
	assert.Contains(t, text, "15 test cases: 9 passed, 6 failed, 0 errored, 0 skipped")
	assert.Contains(t, text, "60.0%", "the run rate is 9 of the 15 it scored")
}
