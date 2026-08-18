// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaieval/internal/pkg/eval_api"

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

	for _, bad := range []string{"passrate=0.8", "pass-rate=abc", "pass-rate=1.5", "pass-rate=-1", "sometimes"} {
		t.Run("refuses "+bad, func(t *testing.T) {
			_, err := parseGate(bad)
			require.Error(t, err)
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

	// Errored rows are inside the total, verified live, so they count against
	// the threshold the same way a failing row does.
	t.Run("errored rows count against the rate", func(t *testing.T) {
		counts := &eval_api.EvalRunResultCounts{Total: 10, Passed: 8, Errored: 2}
		require.Empty(t, eighty.breach(counts), "0.8 exactly meets a 0.8 threshold")

		counts = &eval_api.EvalRunResultCounts{Total: 10, Passed: 7, Errored: 3}
		require.NotEmpty(t, eighty.breach(counts))
	})

	// The wording is pinned because the hero scenario shows it verbatim.
	t.Run("reads as a percentage", func(t *testing.T) {
		counts := &eval_api.EvalRunResultCounts{Total: 1000, Passed: 764}
		require.Equal(t,
			"pass rate 76.4% is below the required 80.0%",
			eighty.breach(counts))
	})

	// A run that scored nothing has no defensible pass rate, and treating it as
	// 100% would let a broken evaluation hold a gate open.
	t.Run("a run that scored nothing breaches", func(t *testing.T) {
		require.NotEmpty(t, eighty.breach(&eval_api.EvalRunResultCounts{Total: 0}))
		require.NotEmpty(t, eighty.breach(nil))
	})
}
