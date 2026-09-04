// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The spec's CI scenario prints this block verbatim, and a pipeline's log is
// where it is read. Both lines are asserted whole: the marker is what tells a
// reader this is a gate rather than a crash, and the ERROR: line is what the
// azd style guide reserves for a terminal failure.
func TestGateBreachMessageMatchesTheScenario(t *testing.T) {
	msg := gateBreachMessage("pass rate 76.0% is below the required 80.0%")

	assert.Equal(t,
		"(x) Failed: Evaluation gate: pass rate 76.0% is below the required 80.0%\n\n"+
			"ERROR: evaluation quality gate not met.\n",
		msg)
}

// Exit 2 is the whole point of --fail-on: a pipeline has to tell "the
// evaluation regressed" apart from "the tool could not run", which is exit 1.
func TestGateBreachUsesItsOwnExitCode(t *testing.T) {
	assert.Equal(t, 2, exitCodeGateBreached,
		"the spec's exit table gives 2 to a breached threshold")
}

// The spec's exit table: a completed run is 0 whatever it scored, and a run
// that errored rather than completed is an operational failure.
func TestRunCompletedSeparatesRegressionFromFailureToRun(t *testing.T) {
	for _, status := range []string{"completed", "Completed", ""} {
		assert.NoErrorf(t, runCompleted(&eval_api.OpenAIEvalRun{ID: "r", Status: status}),
			"%q is a run that produced results, so its score decides the outcome", status)
	}

	for _, status := range []string{"failed", "errored", "canceled"} {
		err := runCompleted(&eval_api.OpenAIEvalRun{ID: "r1", Status: status})
		require.Errorf(t, err, "%q never produced results, so it did not regress", status)
		assert.Contains(t, err.Error(), status)
		assert.Contains(t, err.Error(), "r1")
	}
}

// pass-rate is passed/(passed+failed): the share of the rows something actually
// graded. Errored and skipped rows are outside it, because an infrastructure
// failure is not a quality signal, and this is the figure the portal reports.
//
// The cost is real and deliberate: a run with two passes and thirteen errors
// scores a perfect rate. `any-failure` is the gate that still counts those, and
// a pass-rate gate warns when it judged only part of a run.
func TestPassRateIsMeasuredOverTheRowsThatWereScored(t *testing.T) {
	g, err := parseGate("pass-rate=0.8")
	require.NoError(t, err)

	// 2 passed, 1 failed, 1 errored: 2 of 3 scored is 66.7%, below 80%.
	breach := g.breach(&eval_api.EvalRunResultCounts{Total: 4, Passed: 2, Failed: 1, Errored: 1})
	require.NotEmpty(t, breach, "a scored row that failed still counts")
	assert.Contains(t, breach, "66.7%")

	// The same run without the failure: everything scored, passed, so the
	// errored row does not drag a quality number down on its own.
	assert.Empty(t, g.breach(&eval_api.EvalRunResultCounts{Total: 3, Passed: 2, Errored: 1}),
		"nothing graded the errored row, so it is not evidence of a regression")

	assert.Empty(t, g.breach(&eval_api.EvalRunResultCounts{Total: 3, Passed: 3}))
}

// any-failure is the same concern as the rate above asked as a yes or no, and
// it was the untested half: the gate counts everything that is not a pass, so
// replacing that with the Failed count alone let a run whose rows errored
// report success, and no test noticed.
func TestAnyFailureCountsErroredAndSkippedAsUnpassed(t *testing.T) {
	g, err := parseGate("any-failure")
	require.NoError(t, err)

	assert.NotEmpty(t, g.breach(&eval_api.EvalRunResultCounts{Total: 3, Passed: 2, Errored: 1}),
		"a row that errored did not pass")
	assert.NotEmpty(t, g.breach(&eval_api.EvalRunResultCounts{Total: 3, Passed: 2, Skipped: 1}),
		"a row that was skipped did not pass either")
	assert.NotEmpty(t, g.breach(&eval_api.EvalRunResultCounts{Total: 3, Passed: 2, Failed: 1}))

	assert.Empty(t, g.breach(&eval_api.EvalRunResultCounts{Total: 3, Passed: 3}),
		"every row passed, so there is nothing to report")
}

// A run that scored nothing breaches every threshold rather than dividing by
// zero. "No rows passed" is the honest reading of an empty result.
func TestEmptyRunBreachesEveryThreshold(t *testing.T) {
	g, err := parseGate("pass-rate=0.1")
	require.NoError(t, err)

	breach := g.breach(&eval_api.EvalRunResultCounts{})

	assert.NotEmpty(t, breach)
	assert.NotContains(t, breach, "NaN", "dividing by zero must not reach the message")
}

// --fail-on belongs to the commands that wait for a terminal state, so a
// pipeline that started a run asynchronously can still gate where it reattaches.
func TestFailOnSitsOnTheWaitingCommands(t *testing.T) {
	for _, path := range []string{"run start", "run show"} {
		assert.NotNilf(t, find(t, path).Flags().Lookup("fail-on"),
			"%s waits for a terminal state, so it can gate on one", path)
	}

	usage := find(t, "run start").Flags().Lookup("fail-on").Usage
	for _, form := range []string{"any-failure", "pass-rate"} {
		assert.Containsf(t, usage, form, "--fail-on accepts %q, so its help has to say so", form)
	}
	assert.Contains(t, strings.ToLower(usage), "1",
		"the help has to name the exit code a caller observes, which is the only reason to use the flag")
}

// A run that failed to execute is reported as that, not as a quality breach.
//
// `run show --fail-on` without --wait skipped the status check, so a run whose
// status was `failed` fell through to the gate, which read its empty counts as
// a pass rate of zero and exited "gate breached". A pipeline then went looking
// at the model for a problem the run had never got far enough to have.
func TestAFailedRunIsNotReportedAsAQualityBreach(t *testing.T) {
	failed := &eval_api.OpenAIEvalRun{
		ID:           "evalrun_1",
		Status:       "failed",
		ResultCounts: &eval_api.EvalRunResultCounts{},
	}

	err := runCompleted(failed)
	require.Error(t, err, "an operational failure has to be reported on its own terms")
	assert.Contains(t, err.Error(), "failed")
	assert.NotContains(t, strings.ToLower(err.Error()), "pass rate",
		"the run never scored anything, so a rate says nothing about it")

	// And the run that did finish is left to the gate, which is what judges it.
	require.NoError(t, runCompleted(&eval_api.OpenAIEvalRun{ID: "evalrun_2", Status: "completed"}))
}

// The status check is part of a gate, whether or not the caller waited.
func TestShowGatesOnStatusWheneverAGateIsAskedFor(t *testing.T) {
	cmd := find(t, "run show")
	require.NotNil(t, cmd.Flags().Lookup("fail-on"))
	require.NotNil(t, cmd.Flags().Lookup("wait"),
		"the two are independent: a gate on a terminal run needs no wait")
}
