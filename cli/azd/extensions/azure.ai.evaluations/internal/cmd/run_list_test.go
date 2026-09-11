// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Scenario 3 answers "did my change help?" by reading two rows of `run list`,
// which only works if a row carries when it ran and how it scored. The columns
// were RUN ID / NAME / STATUS / RESULTS, so the question the scenario exists to
// answer could not be.
func TestRunListColumnsMatchTheScenario(t *testing.T) {
	counts := &eval_api.EvalRunResultCounts{Total: 15, Passed: 14, Failed: 1}

	assert.Equal(t, "15", sampleCount(counts),
		"a rate over 15 samples and one over 200 are not the same claim")
	assert.Equal(t, "93.3%", runPassRate(counts),
		"the scenario compares 80.0% against 93.3%, so the row has to carry the rate")
}

// The rate is the gate's arithmetic: passed over the rows that were scored,
// with errored and skipped outside it. A row a reader gates on must not
// disagree with the gate that acts on it.
//
// The list is the one view that shows a rate next to a sample count, so it also
// carries how many rows the rate covers. Without that, two passes and one
// errored row read as SAMPLES 3, PASS RATE 100.0%.
func TestRunListPassRateAgreesWithTheGate(t *testing.T) {
	counts := &eval_api.EvalRunResultCounts{Total: 4, Passed: 2, Failed: 1, Errored: 1}

	assert.Equal(t, "66.7% (3 scored)", runPassRate(counts),
		"2 of the 3 rows that were scored passed, here and in the gate")

	g, err := parseGate("pass-rate=0.8")
	assert.NoError(t, err)
	assert.NotEmpty(t, g.breach(counts),
		"the same counts that read 66.7% must breach an 80% threshold")

	// The errored row is outside the rate rather than counted as a failure, and
	// the cell says so rather than reading as a clean sweep of the run.
	assert.Equal(t, "100.0% (2 scored)",
		runPassRate(&eval_api.EvalRunResultCounts{Total: 3, Passed: 2, Errored: 1}),
		"nothing graded the errored row, so it is not a miss, but the rate is not the whole run")

	// Nothing unscored, nothing to qualify.
	assert.Equal(t, "75.0%",
		runPassRate(&eval_api.EvalRunResultCounts{Total: 4, Passed: 3, Failed: 1}),
		"every row was scored, so the bare rate is the whole story")
}

// A run that has not scored yet has no rate to show. An empty cell says that;
// "0.0%" would say the run failed.
func TestRunListOmitsARateItCannotCompute(t *testing.T) {
	assert.Empty(t, runPassRate(nil))
	assert.Empty(t, runPassRate(&eval_api.EvalRunResultCounts{}))
	assert.Empty(t, sampleCount(nil))
}

// Timestamps are RFC3339 in UTC, whichever shape the service sent. The service
// answers with epoch seconds on some routes and a string on others, and a list
// that renders both would not sort.
func TestRunListTimestampsAreRFC3339UTC(t *testing.T) {
	assert.Equal(t, "2026-08-01T09:15:22Z", timestampString(float64(1785575722)))
	assert.Equal(t, "2026-08-01T09:15:22Z", timestampString(int64(1785575722)))
	assert.Equal(t, "2026-08-01T09:15:22Z", timestampString("2026-08-01T09:15:22Z"))
	assert.Empty(t, timestampString(nil))
}

// The table shows one rate per run because a column per evaluator stops being
// readable as soon as two runs score different evaluators. That makes `-o json`
// the only place a per-evaluator breakdown can be read, and the service does
// return it on the list route, so the runs go out unprojected.
//
// This pins the field name and that it survives marshalling. It does not catch
// someone replacing the emitted type with a projection, which is the way this
// would actually be lost -- that needs the command harness the reconciler tests
// now have.
func TestRunListJSONCarriesThePerEvaluatorBreakdown(t *testing.T) {
	var buf bytes.Buffer
	runs := []eval_api.OpenAIEvalRun{{
		ID: "evalrun_1",
		PerTestingCriteria: []eval_api.EvalRunCriteriaResult{
			{TestingCriteria: "task_adherence", Passed: 14, Failed: 1},
		},
	}}

	require.NoError(t, emitJSONList(&buf, runs))

	assert.Contains(t, buf.String(), `"per_testing_criteria_results"`,
		"the only place a script can read a per-evaluator result")
	assert.Contains(t, buf.String(), "task_adherence")
}
