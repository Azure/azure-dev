// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInvalidRunCountsAreIndeterminateAndNotDisplayedAsRates(t *testing.T) {
	for _, raw := range []string{
		`{"total":1,"passed":2}`,
		`{"total":0,"passed":1}`,
		`{"total":1,"passed":-1}`,
		`{"total":0,"passed":-1}`,
		`{"total":-1,"passed":0}`,
		`{"total":2,"passed":1,"failed":-1}`,
		`{"total":2,"passed":1,"errored":-1}`,
		`{"total":2,"passed":1,"skipped":-1}`,
		`{"total":1,"passed":2,"failed":0,"errored":0,"skipped":0}`,
		`{"total":1,"passed":1,"failed":0,"errored":1,"skipped":0}`,
		`{"total":2,"passed":1,"failed":2,"errored":0,"skipped":0}`,
	} {
		t.Run(raw, func(t *testing.T) {
			var run eval_api.OpenAIEvalRun
			require.NoError(t, json.Unmarshal([]byte(
				`{"id":"run_invalid","eval_id":"eval_invalid","status":"completed","result_counts":`+raw+`}`), &run))
			before, err := json.Marshal(run)
			require.NoError(t, err)
			for _, spec := range []string{"pass-rate=0", "pass-rate=1", "any-failure"} {
				g, err := parseGate(spec)
				require.NoError(t, err)
				reason, err := g.evaluate(&run)
				require.ErrorContains(t, err, "evaluation gate is indeterminate")
				assert.Empty(t, reason, "invalid data must not produce a quality verdict")
			}
			rate, _, available := runPassRateValue(run.ResultCounts)
			assert.False(t, available)
			assert.Zero(t, rate)
			assert.Equal(t, "not reported", reportedRunPassRate(&run))
			for _, render := range []struct {
				name string
				call func(io.Writer, *eval_api.OpenAIEvalRun) error
			}{
				{"summary", func(out io.Writer, run *eval_api.OpenAIEvalRun) error { return renderRun(out, run, nil) }},
				{"detail", renderRunDetail},
				{"output", func(out io.Writer, run *eval_api.OpenAIEvalRun) error {
					return renderResults(out, "eval_invalid", run, nil, resultListView{})
				}},
				{"simulation", func(out io.Writer, run *eval_api.OpenAIEvalRun) error {
					renderConversationResults(out, run)
					return nil
				}},
			} {
				t.Run(render.name, func(t *testing.T) {
					var out bytes.Buffer
					require.NoError(t, render.call(&out, &run))
					if render.name == "detail" {
						assert.Contains(t, out.String(), "Results")
					} else {
						assert.Contains(t, out.String(), "Pass rate  not reported")
					}
					assert.NotContains(t, out.String(), "%")
				})
			}
			after, err := json.Marshal(run)
			require.NoError(t, err)
			assert.JSONEq(t, string(before), string(after), "human validation must preserve service JSON")
		})
	}
}

func TestValidRunRateBoundariesRemainAvailable(t *testing.T) {
	for _, tc := range []struct {
		total, passed int
		rate          float64
	}{
		{1, 0, 0},
		{1, 1, 1},
		{2, 1, 0.5},
	} {
		rate, total, available := runPassRateValue(&eval_api.EvalRunResultCounts{Total: tc.total, Passed: tc.passed})
		assert.True(t, available)
		assert.Equal(t, tc.total, total)
		assert.Equal(t, tc.rate, rate)
	}
}
