// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"testing"

	"azureaieval/internal/exterrors"
	"azureaieval/internal/pkg/eval_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseGate(t *testing.T) {
	t.Run("empty means no gating", func(t *testing.T) {
		g, err := parseGate("")
		require.NoError(t, err)
		require.False(t, g.set)
		require.Empty(t, g.breach(&eval_api.EvalRunResultCounts{Total: 3}),
			"an unset gate must never breach")
	})

	t.Run("any-failure", func(t *testing.T) {
		g, err := parseGate("any-failure")
		require.NoError(t, err)
		require.True(t, g.anyFailure)
	})

	t.Run("pass-rate", func(t *testing.T) {
		g, err := parseGate("pass-rate=0.8")
		require.NoError(t, err)
		require.InDelta(t, 0.8, g.passRate, 1e-9)
	})

	// A --fail-on syntax refusal used to reach -o json with a
	// message and no code at all.
	for _, bad := range []string{"passrate=0.8", "pass-rate=abc", "pass-rate=1.5", "pass-rate=-1", "sometimes"} {
		t.Run("refuses "+bad, func(t *testing.T) {
			_, err := parseGate(bad)
			require.Error(t, err)
			local, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "a --fail-on refusal must carry a structured code")
			assert.Equal(t, exterrors.CodeInvalidParameter, local.Code)
		})
	}
}

func TestGateBreach(t *testing.T) {
	anyFailure, err := parseGate("any-failure")
	require.NoError(t, err)
	eighty, err := parseGate("pass-rate=0.8")
	require.NoError(t, err)

	t.Run("any-failure passes only when every row passed", func(t *testing.T) {
		require.Empty(t, anyFailure.breach(&eval_api.EvalRunResultCounts{Total: 2, Passed: 2}))
		require.NotEmpty(t, anyFailure.breach(&eval_api.EvalRunResultCounts{Total: 2, Passed: 1, Failed: 1}))
	})

	t.Run("all non-passing rows lower the run rate", func(t *testing.T) {
		counts := &eval_api.EvalRunResultCounts{Total: 12, Passed: 8, Failed: 2, Errored: 2}
		require.NotEmpty(t, eighty.breach(counts), "only 8 of all 12 test cases passed")

		counts = &eval_api.EvalRunResultCounts{Total: 13, Passed: 7, Failed: 3, Errored: 3}
		require.NotEmpty(t, eighty.breach(counts), "only 7 of all 13 test cases passed")

		counts = &eval_api.EvalRunResultCounts{Total: 10, Passed: 8, Errored: 2}
		require.Empty(t, eighty.breach(counts), "8 of all 10 test cases passed, which meets 0.8")
	})

	// The wording is pinned because the hero scenario shows it verbatim.
	t.Run("reads as a percentage", func(t *testing.T) {
		counts := &eval_api.EvalRunResultCounts{Total: 1000, Passed: 764, Failed: 236}
		require.Equal(t,
			"pass rate 76.4% is below the required 80.0%",
			eighty.breach(counts))
	})

	// A run with no test cases has no defensible pass rate, and treating it as
	// 100% would let a broken evaluation hold a gate open.
	t.Run("a run with no test cases breaches", func(t *testing.T) {
		require.NotEmpty(t, eighty.breach(&eval_api.EvalRunResultCounts{Total: 0}))
		require.NotEmpty(t, eighty.breach(nil))
	})
}

func TestRunPassRateCountsEveryTerminalOutcome(t *testing.T) {
	eighty, err := parseGate("pass-rate=0.8")
	require.NoError(t, err)

	for _, tc := range []struct {
		name       string
		counts     *eval_api.EvalRunResultCounts
		wantRate   float64
		wantBreach bool
	}{
		{
			name: "passed and errored",
			counts: &eval_api.EvalRunResultCounts{
				Total: 2, Passed: 1, Errored: 1,
			},
			wantRate: 0.5, wantBreach: true,
		},
		{
			name: "passed and failed",
			counts: &eval_api.EvalRunResultCounts{
				Total: 2, Passed: 1, Failed: 1,
			},
			wantRate: 0.5, wantBreach: true,
		},
		{
			name: "passed and skipped",
			counts: &eval_api.EvalRunResultCounts{
				Total: 2, Passed: 1, Skipped: 1,
			},
			wantRate: 0.5, wantBreach: true,
		},
		{
			name: "all errored",
			counts: &eval_api.EvalRunResultCounts{
				Total: 2, Errored: 2,
			},
			wantRate: 0, wantBreach: true,
		},
		{
			name: "all scored meets threshold",
			counts: &eval_api.EvalRunResultCounts{
				Total: 10, Passed: 8, Failed: 2,
			},
			wantRate: 0.8, wantBreach: false,
		},
		{
			name: "all scored below threshold",
			counts: &eval_api.EvalRunResultCounts{
				Total: 10, Passed: 7, Failed: 3,
			},
			wantRate: 0.7, wantBreach: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rate, denominator, ok := runPassRateValue(tc.counts)
			require.True(t, ok)
			assert.Equal(t, tc.counts.Total, denominator)
			assert.InDelta(t, tc.wantRate, rate, 1e-9)
			assert.Equal(t, tc.wantBreach, eighty.breach(tc.counts) != "")
		})
	}
}
