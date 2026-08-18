// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"math"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An evaluator that errored on a row still sends a result, and its score
// decodes to NaN. criteriaMeans has always skipped those -- its comment says
// counting them as zero "would drag the average toward a number no evaluator
// produced" -- but the per-sample column averaged them in, so one errored
// evaluator made the whole sample read NaN.
func TestASampleScoreLeavesOutWhatWasNotScored(t *testing.T) {
	undefined := eval_api.LenientFloat(math.NaN())

	cases := []struct {
		name    string
		results []eval_api.OutputResult
		want    string
	}{
		{
			name:    "no results at all",
			results: nil,
			want:    "-",
		},
		{
			name: "every evaluator scored",
			results: []eval_api.OutputResult{
				{Score: 1.0}, {Score: 0.5},
			},
			want: "0.75",
		},
		{
			name: "one evaluator errored",
			results: []eval_api.OutputResult{
				{Score: 4.0}, {Score: undefined},
			},
			want: "4.00",
		},
		{
			name: "nothing was scored",
			results: []eval_api.OutputResult{
				{Score: undefined}, {Score: undefined},
			},
			want: "-",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, meanScoreOf(tc.results))
		})
	}
}

// The same rule for a single score: a dash says "not scored", where NaN says
// nothing a reader can use.
func TestAScoreThatIsNotANumberShowsAsAbsent(t *testing.T) {
	assert.Equal(t, "-", formatScore(eval_api.LenientFloat(math.NaN())))
	assert.Equal(t, "-", formatScore(eval_api.LenientFloat(math.Inf(1))))
	assert.Equal(t, "0.75", formatScore(eval_api.LenientFloat(0.75)))
	assert.Equal(t, "0.00", formatScore(eval_api.LenientFloat(0)),
		"a real zero is a score and must still be shown")
}

// The gate compares exact values while the message rounds, so a rate just under
// the threshold could be reported as below itself. The spec's hero scenario
// shows one decimal, so that is kept for every case where the two differ.
func TestAGateBreachNeverReportsARateAsBelowItself(t *testing.T) {
	gate, err := parseGate("pass-rate=0.8")
	require.NoError(t, err)

	breach := gate.breach(&eval_api.EvalRunResultCounts{Total: 10000, Passed: 7996})
	require.NotEmpty(t, breach, "7996/10000 is under 0.8 and must breach")
	assert.NotContains(t, breach, "80.0% is below the required 80.0%",
		"a line saying a rate is below itself tells a reader nothing")
	assert.Contains(t, breach, "79.96%")

	// The wording the spec shows is unchanged wherever rounding does not collide.
	assert.Equal(t,
		"pass rate 76.4% is below the required 80.0%",
		gate.breach(&eval_api.EvalRunResultCounts{Total: 1000, Passed: 764}))
}
