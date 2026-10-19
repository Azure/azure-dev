// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// All terminal rows remain in the denominator, including outcomes that were
// not scored or are not accounted for by the reported breakdown.
func TestRunGateDenominatorPolicy(t *testing.T) {
	binary, err := os.Executable()
	require.NoError(t, err)

	for _, tc := range []struct {
		name, counts, spec, outcome, warning string
		total                                int
		rate                                 float64
	}{
		{
			name:    "mostly errored without a gate",
			counts:  `{"total":4,"passed":1,"failed":0,"errored":3,"skipped":0}`,
			outcome: "pass", total: 4, rate: 0.25,
		},
		{
			name:   "mostly errored pass-rate",
			counts: `{"total":4,"passed":1,"failed":0,"errored":3,"skipped":0}`,
			spec:   "pass-rate=0.8", outcome: "breach", total: 4, rate: 0.25,
		},
		{
			name:   "mostly errored any-failure",
			counts: `{"total":4,"passed":1,"failed":0,"errored":3,"skipped":0}`,
			spec:   "any-failure", outcome: "breach", total: 4, rate: 0.25,
		},
		{
			name:   "mostly skipped pass-rate",
			counts: `{"total":4,"passed":1,"failed":0,"errored":0,"skipped":3}`,
			spec:   "pass-rate=0.8", outcome: "breach", total: 4, rate: 0.25,
		},
		{
			name:   "mostly skipped any-failure",
			counts: `{"total":4,"passed":1,"failed":0,"errored":0,"skipped":3}`,
			spec:   "any-failure", outcome: "breach", total: 4, rate: 0.25,
		},
		{
			name:   "mixed failed and errored",
			counts: `{"total":4,"passed":2,"failed":1,"errored":1,"skipped":0}`,
			spec:   "pass-rate=0.8", outcome: "breach", total: 4, rate: 0.5,
		},
		{
			name:   "exact threshold",
			counts: `{"total":5,"passed":4,"failed":1,"errored":0,"skipped":0}`,
			spec:   "pass-rate=0.8", outcome: "pass", total: 5, rate: 0.8,
		},
		{
			name:   "below threshold",
			counts: `{"total":5,"passed":3,"failed":2,"errored":0,"skipped":0}`,
			spec:   "pass-rate=0.8", outcome: "breach", total: 5, rate: 0.6,
		},
		{
			name:   "all passed maximum threshold",
			counts: `{"total":4,"passed":4,"failed":0,"errored":0,"skipped":0}`,
			spec:   "pass-rate=1", outcome: "pass", total: 4, rate: 1,
		},
		{
			name:   "all failed zero threshold",
			counts: `{"total":4,"passed":0,"failed":4,"errored":0,"skipped":0}`,
			spec:   "pass-rate=0", outcome: "pass", total: 4,
		},
		{
			name:   "all errored zero threshold",
			counts: `{"total":4,"passed":0,"failed":0,"errored":4,"skipped":0}`,
			spec:   "pass-rate=0", outcome: "pass", total: 4,
		},
		{
			name:   "all skipped",
			counts: `{"total":4,"passed":0,"failed":0,"errored":0,"skipped":4}`,
			spec:   "pass-rate=0.8", outcome: "breach", total: 4,
		},
		{
			name:   "empty zero threshold",
			counts: `{"total":0,"passed":0,"failed":0,"errored":0,"skipped":0}`,
			spec:   "pass-rate=0", outcome: "breach",
		},
		{
			name:   "unaccounted rows",
			counts: `{"total":4,"passed":1,"failed":0,"errored":0,"skipped":0}`,
			spec:   "pass-rate=0.8", outcome: "breach", total: 4, rate: 0.25,
			warning: "3 of 4 rows are not accounted for by the reported outcome counts; " +
				"the pass-rate denominator still includes all 4 rows",
		},
		{
			name:   "unreported total pass-rate",
			counts: `{"passed":1,"failed":0}`,
			spec:   "pass-rate=0.8", outcome: "indeterminate",
		},
		{
			name:   "unreported total any-failure",
			counts: `{"passed":1,"failed":0}`,
			spec:   "any-failure", outcome: "indeterminate",
		},
		{
			name:   "unreported failed",
			counts: `{"total":4,"passed":1,"errored":3,"skipped":0}`,
			spec:   "pass-rate=0.8", outcome: "breach", total: 4, rate: 0.25,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := `{"id":"run_counts","status":"completed","result_counts":` + tc.counts + `}`
			var run eval_api.OpenAIEvalRun
			require.NoError(t, json.Unmarshal([]byte(response), &run))
			rate, total, known := runPassRateValue(run.ResultCounts)
			assert.Equal(t, tc.total, total)
			assert.Equal(t, tc.total > 0, known)
			assert.InDelta(t, tc.rate, rate, 1e-9)

			for _, caller := range []string{"start", "show"} {
				for _, format := range []string{"table", "json"} {
					t.Run(caller+"/"+format, func(t *testing.T) {
						dir := t.TempDir()
						// Reuse the existing localhost command fixture and child
						// process, since a determinate breach calls os.Exit.
						child := exec.CommandContext(t.Context(), binary,
							"-test.run=^TestRunGatesPreserveCountPresence$")
						child.Env = append(os.Environ(), "AZD_TEST_GATE_PRESENCE_DIR="+dir, "NO_COLOR=1",
							"AZD_TEST_GATE_RESPONSE="+response, "AZD_TEST_GATE_SPEC="+tc.spec,
							"AZD_TEST_GATE_OUTCOME="+tc.outcome,
							"AZD_TEST_GATE_CALLER="+caller, "AZD_TEST_GATE_FORMAT="+format)
						output, err := child.CombinedOutput()
						if tc.outcome == "breach" {
							exitErr, ok := errors.AsType[*exec.ExitError](err)
							require.True(t, ok, "expected gate exit, got %v: %s", err, output)
							assert.Equal(t, exitCodeGateBreached, exitErr.ExitCode(), "%s", output)
						} else {
							require.NoError(t, err, "%s", output)
						}

						stdout, err := os.ReadFile(filepath.Join(dir, "stdout.txt"))
						require.NoError(t, err)
						stderr, err := os.ReadFile(filepath.Join(dir, "stderr.txt"))
						require.NoError(t, err)
						if format == "json" {
							assert.JSONEq(t, response, string(stdout))
						} else if caller == "start" {
							assert.Contains(t, string(stdout), "TEST CASE RESULTS")
							assert.Contains(t, string(stdout), "Pass rate")
							if _, reported := run.ReportedResultCounts()["total"]; !reported {
								assert.Contains(t, string(stdout), "Pass rate  not reported")
							} else if tc.total == 0 {
								assert.Regexp(t, `Pass rate\s+-`, string(stdout))
							} else if tc.outcome != "indeterminate" {
								assert.Contains(t, string(stdout), passRateText(tc.rate, true))
								assert.Contains(t, string(stdout), "total test cases")
							}
						}
						if tc.warning == "" {
							assert.NotContains(t, string(stderr), "warning:")
						} else {
							assert.Contains(t, string(stderr), "warning: "+tc.warning)
							assert.NotContains(t, string(stdout), tc.warning)
						}
						if tc.outcome == "breach" {
							assert.Contains(t, string(stderr), "ERROR: evaluation quality gate not met.")
						} else {
							assert.NotContains(t, string(stderr), "quality gate not met")
						}
					})
				}
			}
		})
	}
}
