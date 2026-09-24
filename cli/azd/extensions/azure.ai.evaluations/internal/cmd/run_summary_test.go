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

// finishedRun is a run the way the service returns one: counts over samples,
// and a result per testing criterion.
func finishedRun() *eval_api.OpenAIEvalRun {
	return &eval_api.OpenAIEvalRun{
		ID:     "evalrun_abc123",
		Status: "completed",
		ResultCounts: &eval_api.EvalRunResultCounts{
			Total: 10, Passed: 7, Failed: 3,
		},
		PerTestingCriteria: []eval_api.EvalRunCriteriaResult{
			{TestingCriteria: "relevance", Passed: 9, Failed: 1},
			{TestingCriteria: "coherence", Passed: 7, Failed: 3},
		},
	}
}

// The whole point of waiting for a run is the verdict per evaluator. Printing
// only the status meant the answer to the question the command was asked took
// a second command to see.
func TestRenderRunReportsEveryEvaluator(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, renderRun(&out, finishedRun(), nil))
	text := out.String()

	assert.Contains(t, text, "evalrun_abc123")
	assert.Contains(t, text, "completed")

	for _, criterion := range []string{"relevance", "coherence"} {
		assert.Contains(t, text, criterion,
			"every evaluator the run scored must appear")
	}
	assert.Contains(t, text, "90.0%", "relevance passed 9 of 10")
	assert.Contains(t, text, "70.0%", "coherence passed 7 of 10")
	assert.Contains(t, text, "(7 / (7 passed + 3 failed))",
		"the counts behind the rate must be shown, and the denominator named: "+
			"a bare 7/10 does not say whether the 3 failed or never ran")
}

// The two tables count different things, so each says which.
//
// One test case that failed two evaluators is one row to go and look at and
// two failing verdicts. Printed side by side and unlabelled, they read as the
// same number disagreeing with itself.
func TestRenderRunSeparatesTestCasesFromEvaluatorResults(t *testing.T) {
	run := finishedRun()
	run.PerTestingCriteria = []eval_api.EvalRunCriteriaResult{
		{TestingCriteria: "relevance", Passed: 12, Failed: 2, Skipped: 1, Errored: 0},
	}

	var out bytes.Buffer
	require.NoError(t, renderRun(&out, run, nil))
	text := out.String()

	assert.Contains(t, text, "TEST CASE RESULTS")
	assert.Contains(t, text, "EVALUATOR RESULTS")
	assert.Less(t, strings.Index(text, "TEST CASE RESULTS"), strings.Index(text, "EVALUATOR RESULTS"),
		"the run's own outcome comes before the breakdown of it")

	// A skip is not an error and neither is a failure, so each has a column
	// rather than being folded into FAIL where it reads as a quality problem.
	assert.Contains(t, text, "PASS  FAIL  SKIP  ERROR")
	assert.Contains(t, text, "SCORED")
	assert.Contains(t, text, "14/15",
		"the scored count says how much of the run that evaluator actually judged")
}

// Two runs of the same eval have to read the same way. The service returns the
// criteria in whatever order it evaluated them, which is not stable.
func TestRenderRunOrdersEvaluatorsByName(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, renderRun(&out, finishedRun(), nil))

	text := out.String()
	assert.Less(t, strings.Index(text, "coherence"), strings.Index(text, "relevance"),
		"evaluators must be listed in a stable order")
}

// An errored row is not a failing row: the evaluator never reached a verdict.
// Folding the two together would report a service problem as a quality problem.
func TestRenderRunSeparatesErrorsFromFailures(t *testing.T) {
	run := finishedRun()
	run.ResultCounts = &eval_api.EvalRunResultCounts{Total: 10, Passed: 7, Failed: 1, Errored: 2}
	run.PerTestingCriteria = []eval_api.EvalRunCriteriaResult{
		{TestingCriteria: "relevance", Passed: 7, Failed: 1, Errored: 2},
	}

	var out bytes.Buffer
	require.NoError(t, renderRun(&out, run, nil))
	text := out.String()

	assert.Contains(t, text, "Errored       2",
		"a run that errored on two rows says so in its own line, not as a footnote")
	assert.Contains(t, text, "87.5%",
		"the pass rate is over what was scored, not over what was attempted")
	assert.Contains(t, text, "(7 / (7 passed + 1 failed))",
		"and names the two rows it divided, so the errored two are visibly outside it")
}

// A rate over nothing is not zero. Printing 0.0% for a criterion that scored
// no rows reads as a total failure rather than as no data.
func TestFormatRateHasNoOpinionAboutNothing(t *testing.T) {
	assert.Equal(t, "-", formatRate(0, 0))
	assert.Equal(t, "0.0%", formatRate(0, 4))
	assert.Equal(t, "100.0%", formatRate(4, 4))
	assert.Equal(t, "33.3%", formatRate(1, 3))
}

// The next thing anyone does after seeing failures is look at them, so the
// command that shows them is named — and it has to be a command that exists,
// carrying the ids it needs so it can be run as printed.
func TestRenderRunPointsAtTheFailingSamples(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, renderRun(&out, finishedRun(), nil))
	assert.Contains(t, out.String(), "azd ai eval run output list")
	assert.Contains(t, out.String(), "--failed-only")
	assert.Contains(t, out.String(), "--run evalrun_abc123",
		"a command missing the run it refers to has to be retyped before it works")
	assert.Contains(t, out.String(), "azd ai eval run output export",
		"reading the whole run is the other half of investigating one")

	clean := finishedRun()
	clean.ResultCounts = &eval_api.EvalRunResultCounts{Total: 10, Passed: 10}
	clean.PerTestingCriteria = []eval_api.EvalRunCriteriaResult{
		{TestingCriteria: "relevance", Passed: 10},
	}
	var cleanOut bytes.Buffer
	require.NoError(t, renderRun(&cleanOut, clean, nil))
	assert.NotContains(t, cleanOut.String(), "--failed-only",
		"a run with nothing to look at must not send anyone looking")
	assert.NotContains(t, cleanOut.String(), "Investigate:",
		"nothing failed and nothing errored, so there is nothing to investigate")
}

// A run whose rows errored closed with the word "failed" and a count, and
// nothing saying where to look. Errored rows are not offered --failed-only,
// because that filter holds back exactly the rows nothing scored. ADO 5595070.
func TestRenderRunPointsSomewhereUsefulWhenRowsErrored(t *testing.T) {
	run := finishedRun()
	run.ResultCounts = &eval_api.EvalRunResultCounts{Total: 10, Passed: 4, Failed: 0, Errored: 6}
	run.PerTestingCriteria = []eval_api.EvalRunCriteriaResult{
		{TestingCriteria: "relevance", Passed: 4, Errored: 6},
	}

	var out bytes.Buffer
	require.NoError(t, renderRun(&out, run, nil))

	text := out.String()
	assert.Contains(t, text, "Investigate:")
	assert.Contains(t, text, "azd ai eval run output list")
	assert.Contains(t, text, "azd ai eval run output export")
	assert.NotContains(t, text, "--failed-only",
		"nothing failed; --failed-only would open an empty list for the run that most needs reading")
}

// A run that never produced counts still has to render. The service returns
// none for a run that failed before scoring, and a nil dereference there would
// replace the failure message with a panic.
func TestRenderRunSurvivesAnEmptyResult(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, renderRun(&out, &eval_api.OpenAIEvalRun{ID: "evalrun_x", Status: "failed"}, nil))
	assert.Contains(t, out.String(), "evalrun_x")
}
