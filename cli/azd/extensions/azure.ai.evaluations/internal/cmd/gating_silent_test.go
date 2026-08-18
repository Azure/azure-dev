// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A gate is the whole reason a pipeline runs this command, so the ways it can
// silently stop gating matter more than the ways it can fire.

// NaN parses, clears both range checks, and then loses every comparison it is
// put in. A pipeline written this way believes it is gated and is not.
func TestFailOnRejectsAThresholdThatCanNeverFire(t *testing.T) {
	for _, spec := range []string{"pass-rate=NaN", "pass-rate=nan", "pass-rate=-nan"} {
		_, err := parseGate(spec)
		require.Errorf(t, err, "%s would disable the gate while looking like one", spec)
	}

	// The range check already covers infinities; this pins that it still does.
	for _, spec := range []string{"pass-rate=Inf", "pass-rate=-Inf", "pass-rate=1.5", "pass-rate=-0.1"} {
		_, err := parseGate(spec)
		require.Errorf(t, err, "%s is not a pass rate", spec)
	}

	g, err := parseGate("pass-rate=0.8")
	require.NoError(t, err)
	assert.True(t, g.set)
	assert.InDelta(t, 0.8, g.passRate, 1e-9)
}

// A run that graded nothing has not passed. The pass-rate gate always said so;
// any-failure computed Total-Passed, which is zero for an empty run, and let it
// through -- the one shape a gate exists to catch.
func TestEveryGateBreachesOnARunThatScoredNothing(t *testing.T) {
	empty := &eval_api.EvalRunResultCounts{Total: 0}

	anyFailure, err := parseGate("any-failure")
	require.NoError(t, err)
	assert.NotEmpty(t, anyFailure.breach(empty),
		"an empty run must not clear an any-failure gate")

	rate, err := parseGate("pass-rate=0.8")
	require.NoError(t, err)
	assert.NotEmpty(t, rate.breach(empty))

	// A gate that was never asked for stays silent whatever the counts.
	assert.Empty(t, gate{}.breach(empty))
}

// The ordinary cases still behave, so the empty-run guard did not swallow them.
func TestGatesStillJudgeRunsThatScoredSomething(t *testing.T) {
	anyFailure, err := parseGate("any-failure")
	require.NoError(t, err)

	assert.Empty(t, anyFailure.breach(&eval_api.EvalRunResultCounts{Total: 3, Passed: 3}),
		"every row passed, so there is nothing to report")
	assert.NotEmpty(t, anyFailure.breach(&eval_api.EvalRunResultCounts{Total: 3, Passed: 2}),
		"one row did not pass")

	rate, err := parseGate("pass-rate=0.8")
	require.NoError(t, err)
	assert.Empty(t, rate.breach(&eval_api.EvalRunResultCounts{Total: 10, Passed: 9}))
	assert.NotEmpty(t, rate.breach(&eval_api.EvalRunResultCounts{Total: 10, Passed: 7}))

	// Counts the service never sent are not a pass.
	assert.NotEmpty(t, rate.breach(nil))
}

// The spec puts --fail-on on the commands that wait. A run still in progress
// has partial counts, so gating it can fail a run that would have passed --
// and silently skipping the gate would leave a pipeline believing it is
// protected when it is not.
func TestOnlyATerminalRunCanBeGated(t *testing.T) {
	// Read from the polling vocabulary instead of a second copy of it. Spelling
	// the list out here is what hid "error" being gateable: the test agreed with
	// the bug.
	for status := range terminalRunStates {
		assert.Truef(t, runIsTerminal(&eval_api.OpenAIEvalRun{Status: status}),
			"the poller stops on %q, so its counts are final", status)
	}
	assert.True(t, runIsTerminal(&eval_api.OpenAIEvalRun{Status: ""}),
		"a run the service reported no status for is gated on the counts it gave")

	for _, status := range []string{"in_progress", "queued", "running"} {
		assert.Falsef(t, runIsTerminal(&eval_api.OpenAIEvalRun{Status: status}),
			"%q is still moving, so its counts are partial", status)
	}

	assert.False(t, runIsTerminal(nil), "no run is not a finished run")

	err := messages.GateNeedsATerminalRun("evalrun_1", "in_progress")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--wait", "the way out has to be named")
}
