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

// pass-rate is passed/total, and the service puts errored and skipped rows
// inside total. Were they outside it, a run with two passes and one error
// would score a perfect rate -- exactly the broken evaluation a gate exists to
// catch.
func TestPassRateCountsErroredAndSkippedAgainstTheThreshold(t *testing.T) {
	g, err := parseGate("pass-rate=0.8")
	require.NoError(t, err)

	// 2 passed of 3 total, the third errored: 66.7%, below 80%.
	breach := g.breach(&eval_api.EvalRunResultCounts{Total: 3, Passed: 2, Errored: 1})
	require.NotEmpty(t, breach, "an errored row is not a pass")
	assert.Contains(t, breach, "66.7%")

	assert.Empty(t, g.breach(&eval_api.EvalRunResultCounts{Total: 3, Passed: 3}))
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
