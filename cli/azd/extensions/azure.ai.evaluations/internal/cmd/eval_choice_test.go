// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// configWith builds a configuration declaring the named evals.
func configWith(names ...string) *project.EvalConfig {
	cfg := &project.EvalConfig{}
	for _, n := range names {
		cfg.Evals = append(cfg.Evals, project.Eval{Name: n})
	}
	return cfg
}

// chooseEval only asks when asking can settle something. Every case below
// resolves without a prompt, so none of them reaches the azd client.
func TestChooseEvalOnlyAsksWhenThereIsAChoice(t *testing.T) {
	t.Run("a name given is never second-guessed", func(t *testing.T) {
		got := chooseEval(newEvalCreateCommand(), configWith("a", "b"), "b")
		assert.Equal(t, "b", got)
	})

	t.Run("one declared eval needs no question", func(t *testing.T) {
		got := chooseEval(newEvalCreateCommand(), configWith("only"), "")
		assert.Empty(t, got, "the caller resolves the single eval, so nothing is chosen here")
	})

	t.Run("no configuration is left to the caller", func(t *testing.T) {
		assert.Empty(t, chooseEval(newEvalCreateCommand(), nil, ""))
	})

	t.Run("--no-prompt keeps the error", func(t *testing.T) {
		cmd := newEvalCreateCommand()
		cmd.Flags().Bool("no-prompt", true, "")

		got := chooseEval(cmd, configWith("a", "b"), "")

		assert.Empty(t, got,
			"there is nobody to ask, so the command must still refuse rather than guess")
	})
}

// Returning the name unchanged is what leaves the existing error in place, and
// that error is the one users praised: it counts the evals and names them all.
func TestSeveralEvalsErrorStillNamesEveryCandidate(t *testing.T) {
	cfg := configWith("obs-trace-eval", "obs-eval")
	cmd := newEvalCreateCommand()
	// Without this the picker reaches azdext.NewAzdClient and attempts a real
	// RPC, which passes only because resolving an empty address fails fast.
	// This test is about the message, not about network behaviour.
	cmd.Flags().Bool("no-prompt", true, "")

	_, err := cfg.Eval(chooseEval(cmd, cfg, ""))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "obs-trace-eval")
	assert.Contains(t, err.Error(), "obs-eval")
	assert.Contains(t, err.Error(), "--eval")
}

// A directory with no configuration must not turn into a prompt, and must not
// swallow the error the command that opens it properly will raise.
func TestChooseEvalInLeavesAnAbsentConfigAlone(t *testing.T) {
	assert.Empty(t, chooseEvalIn(newEvalCreateCommand(), t.TempDir(), ""))
	assert.Equal(t, "named", chooseEvalIn(newEvalCreateCommand(), t.TempDir(), "named"))
}
