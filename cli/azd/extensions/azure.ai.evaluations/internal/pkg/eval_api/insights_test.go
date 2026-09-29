// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The exact body the service returned for a comparison of two single-sample
// runs. `standardDeviation` is the string "NaN" because JSON has no NaN
// literal, and decoding it into a float64 failed the whole comparison — losing
// the TooFewSamples verdict that explains the very situation that produced it.
const oneSampleComparison = `{
  "comparisons": [
    {
      "testingCriteria": "task_adherence",
      "metric": "task_adherence",
      "evaluator": "builtin.task_adherence",
      "baselineRunSummary": {
        "runId": "evalrun_base",
        "sampleCount": 1,
        "average": 1.0,
        "standardDeviation": "NaN"
      },
      "compareItems": [
        {
          "treatmentRunSummary": {
            "runId": "evalrun_treat",
            "sampleCount": 1,
            "average": 1.0,
            "standardDeviation": "NaN"
          },
          "deltaEstimate": 0.0,
          "pValue": 1.0,
          "treatmentEffect": "TooFewSamples"
        }
      ]
    }
  ],
  "method": "TTest",
  "type": "EvaluationComparison"
}`

func TestInsightResult_DecodesQuotedNaN(t *testing.T) {
	var got InsightResult
	require.NoError(t, json.Unmarshal([]byte(oneSampleComparison), &got))

	require.Len(t, got.Comparisons, 1)
	c := got.Comparisons[0]
	require.NotNil(t, c.BaselineRunSummary)

	assert.Equal(t, 1.0, float64(c.BaselineRunSummary.Average))
	assert.False(t, c.BaselineRunSummary.StandardDeviation.Defined(),
		"a single sample has no standard deviation")

	require.Len(t, c.CompareItems, 1)
	assert.Equal(t, "TooFewSamples", c.CompareItems[0].TreatmentEffect,
		"the verdict survives, which is the whole point of not failing the parse")
	assert.Equal(t, 1.0, float64(c.CompareItems[0].PValue))
}

func TestLenientFloat_AcceptsBothShapes(t *testing.T) {
	cases := map[string]func(LenientFloat) bool{
		`0.75`:        func(f LenientFloat) bool { return float64(f) == 0.75 },
		`"0.75"`:      func(f LenientFloat) bool { return float64(f) == 0.75 },
		`"NaN"`:       func(f LenientFloat) bool { return !f.Defined() },
		`"Infinity"`:  func(f LenientFloat) bool { return !f.Defined() },
		`"-Infinity"`: func(f LenientFloat) bool { return !f.Defined() },
		`null`:        func(f LenientFloat) bool { return !f.Defined() },
		`""`:          func(f LenientFloat) bool { return !f.Defined() },
	}

	for raw, ok := range cases {
		var f LenientFloat
		require.NoError(t, json.Unmarshal([]byte(raw), &f), "decoding %s", raw)
		assert.True(t, ok(f), "unexpected value decoding %s", raw)
	}

	var f LenientFloat
	assert.Error(t, json.Unmarshal([]byte(`"not a number"`), &f),
		"genuine garbage must still be reported")
}

// encoding/json refuses to marshal NaN, so `-o json` would fail on any
// comparison holding one unless it is written as null.
func TestLenientFloat_MarshalsNonFiniteAsNull(t *testing.T) {
	b, err := json.Marshal(LenientFloat(math.NaN()))
	require.NoError(t, err)
	assert.Equal(t, "null", string(b))

	b, err = json.Marshal(LenientFloat(0.5))
	require.NoError(t, err)
	assert.Equal(t, "0.5", string(b))

	// The whole result has to survive a round trip, since that is what
	// `results compare -o json` emits.
	var res InsightResult
	require.NoError(t, json.Unmarshal([]byte(oneSampleComparison), &res))
	out, err := json.Marshal(res)
	require.NoError(t, err, "a comparison containing NaN must still emit JSON")
	assert.Contains(t, string(out), `"standardDeviation":null`)
}
