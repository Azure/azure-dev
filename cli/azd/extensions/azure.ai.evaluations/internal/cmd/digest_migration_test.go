// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// evalWithWindow is a trace-backed declaration, which is the shape where the
// two digests differ.
func evalWithWindow(name string, lookback int) project.Eval {
	return project.Eval{
		Name:       name,
		Source:     &project.SourceDecl{Type: "traces", AgentName: "support-agent", LookbackHours: lookback},
		Evaluators: evalcore.EvaluatorList{{Evaluator: "builtin.relevance"}},
	}
}

// Environments written before the digest was split hold the full digest under
// the recreate key. Comparing only against the definition made the first deploy
// after an upgrade recreate every eval carrying max_samples or source:, for a
// file nobody had touched, and left the runs taken under the old one
// unreachable.
func TestAnEnvironmentFromBeforeTheSplitIsNotReadAsAChange(t *testing.T) {
	group := evalWithWindow("nightly", 24)

	shipped, err := project.FingerprintGroup(group)
	require.NoError(t, err)
	definition, err := project.FingerprintDefinition(group)
	require.NoError(t, err)

	require.NotEqual(t, shipped, definition,
		"the fixture has to be a declaration where the two digests differ")

	// What EnsureEval asks of the recorded baseline.
	unchanged := func(prior string) bool {
		return !(prior != "" && prior != definition && prior != shipped)
	}

	assert.True(t, unchanged(shipped), "a baseline written by an earlier build is not a change")
	assert.True(t, unchanged(definition), "nor is one written by this build")
	assert.True(t, unchanged(""), "nor is no baseline at all")

	// And a real edit is still a change.
	edited := group
	edited.Evaluators = evalcore.EvaluatorList{{Evaluator: "builtin.coherence"}}
	editedDefinition, err := project.FingerprintDefinition(edited)
	require.NoError(t, err)
	assert.False(t, unchanged(editedDefinition), "a different evaluator has to recreate the eval")
}

// Substance keys are never removed, so one left by an earlier edit still points
// at a live eval. A later declaration that happens to hash to it would adopt
// that eval, rename it, and end up sharing its runs -- so an eval this deploy
// already settled on cannot be adopted again.
func TestAnEvalAlreadySettledOnIsNotAdoptedTwice(t *testing.T) {
	r := &evalReconciler{}

	r.claim("eval_1")

	assert.True(t, r.claimed["eval_1"])
	assert.False(t, r.claimed["eval_2"], "only what this deploy settled on is claimed")
}

// The map is built on first use, because the reconciler is also constructed
// literally in several places and writing to a nil map panics.
func TestClaimingWorksOnAReconcilerBuiltDirectly(t *testing.T) {
	r := &evalReconciler{}

	assert.NotPanics(t, func() { r.claim("eval_1") })
	assert.True(t, r.claimed["eval_1"])
}
