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
				{Name: "relevance", Passed: true, Score: 5},
				{Name: "coherence", Passed: true, Score: 4},
			},
		},
		{
			ID: "oi_2",
			Results: []eval_api.OutputResult{
				{Name: "relevance", Passed: false, Score: 1, Reason: "Answered a different question."},
				{Name: "coherence", Passed: false, Score: 2, Reason: "Rambled."},
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
	require.NoError(t, renderResults(&out, run, scoredRows(), false))

	text := out.String()
	assert.Equal(t, 1, strings.Count(text, "oi_2"),
		"a sample that failed two evaluators must still be one row:\n%s", text)

	for _, header := range []string{"ITEM", "SAMPLE", "FAILED EVALUATORS", "REASON (first failure)"} {
		assert.Containsf(t, text, header, "the listing lost its %s column", header)
	}
}

// The failing row has to name every evaluator that failed it, because that is
// what says whether the sample is broken or one evaluator is.
func TestRenderResultsNamesEveryFailedEvaluator(t *testing.T) {
	var out bytes.Buffer
	run := &eval_api.OpenAIEvalRun{ID: "evalrun_1", Status: "completed"}
	require.NoError(t, renderResults(&out, run, scoredRows(), true))

	text := out.String()
	assert.Contains(t, text, "relevance, coherence")
	assert.Contains(t, text, "Answered a different question.",
		"the first failure's reason is what the row is looked at for")
	assert.NotContains(t, text, "oi_1", "--failed-only must drop the passing sample")
	assert.Contains(t, text, "1 sample(s) failed at least one evaluator.")
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
		{Results: []eval_api.OutputResult{{Name: "relevance", Score: 4, Passed: true}}},
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
		"the declared name is what the author recognises, not the service id")
	assert.Contains(t, text, "Status     completed")
	assert.Contains(t, text, "Samples    15")
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
