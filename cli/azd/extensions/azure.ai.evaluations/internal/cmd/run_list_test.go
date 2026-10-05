// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
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

// The rate is the gate's arithmetic: passed over every test case in the run. A
// row a reader gates on must not disagree with the gate that acts on it.
func TestRunListPassRateAgreesWithTheGate(t *testing.T) {
	counts := &eval_api.EvalRunResultCounts{Total: 4, Passed: 2, Failed: 1, Errored: 1}

	assert.Equal(t, "50.0%", runPassRate(counts),
		"2 of all 4 test cases passed, here and in the gate")

	g, err := parseGate("pass-rate=0.8")
	assert.NoError(t, err)
	assert.NotEmpty(t, g.breach(counts),
		"the same counts that read 50.0% must breach an 80% threshold")

	assert.Equal(t, "66.7%",
		runPassRate(&eval_api.EvalRunResultCounts{Total: 3, Passed: 2, Errored: 1}),
		"the errored row did not pass, so it lowers the run-level rate")

	// The all-scored control keeps the same result.
	assert.Equal(t, "75.0%",
		runPassRate(&eval_api.EvalRunResultCounts{Total: 4, Passed: 3, Failed: 1}),
		"the all-scored control keeps the same arithmetic")
}

// A run that has no reported test cases has no rate to show. An empty cell says that;
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

func TestRunNumberTimestampsKeepHumanFormattingAndOrdering(t *testing.T) {
	for _, value := range []string{"1785575722", "1785575722.75", "1.785575722e9"} {
		t.Run(value, func(t *testing.T) {
			var run eval_api.OpenAIEvalRun
			require.NoError(t, json.Unmarshal([]byte(`{"id":"new","created_at":`+value+
				`,"modified_at":1785575782}`), &run))
			assert.Equal(t, "2026-08-01T09:15:22Z", timestampString(run.CreatedAt))
			assert.Equal(t, "1m00s", runDuration(&run))
			assert.Equal(t, "new", newestRunIn([]eval_api.OpenAIEvalRun{
				run, {ID: "old", CreatedAt: "2026-08-01T09:15:21Z"},
			}).ID)
			assert.Equal(t, "2026-08-01T09:15:22Z", startedRun(&run, "eval", nil).CreatedAt)
		})
	}
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
