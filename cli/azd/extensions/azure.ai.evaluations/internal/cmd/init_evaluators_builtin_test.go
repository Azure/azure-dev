// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"
	"time"

	"azureaieval/internal/pkg/evalcore"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The catalogue is the only thing that can say whether a builtin. reference
// names something real. init used to accept any of them and fail at create.
// ADO 5631310.
func TestRefuseUnknownBuiltins_RefusesANameTheCatalogueDoesNotOffer(t *testing.T) {
	t.Parallel()

	known := []string{"builtin.coherence", "builtin.relevance", "builtin.task_completion"}

	err := refuseUnknownBuiltins([]string{"builtin.does_not_exist"}, known)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "builtin.does_not_exist")
	assert.Contains(t, err.Error(), "builtin.relevance",
		"the error lists what the project does offer")
}

// An unreadable catalogue is the offline case, and it must behave exactly as
// init did before: no opinion, reference left as written. Refusing here would
// turn every disconnected init into a failure, which is worse than the bug.
func TestRefuseUnknownBuiltins_NoCatalogueMeansNoOpinion(t *testing.T) {
	t.Parallel()

	for _, known := range [][]string{nil, {}} {
		assert.NoError(t, refuseUnknownBuiltins(
			[]string{"builtin.does_not_exist", "builtin.anything_at_all"}, known),
			"an unread catalogue cannot refuse anything")
	}
}

// A builtin the picker never offers is still valid -- the offered four are a
// subset of the catalogue, which is the whole reason a local list cannot be the
// check.
func TestRefuseUnknownBuiltins_AcceptsBuiltinsOutsideTheOfferedFour(t *testing.T) {
	t.Parallel()

	known := []string{
		"builtin.coherence", "builtin.fluency", "builtin.relevance",
		"builtin.similarity", "builtin.task_adherence",
	}

	for _, ref := range []string{"builtin.relevance", "builtin.similarity", "builtin.task_adherence"} {
		assert.NoError(t, refuseUnknownBuiltins([]string{ref}, known),
			"%s is in the catalogue even though init does not offer it", ref)
	}
}

// A bare name is a rubric this configuration declares or will generate. The
// built-in catalogue says nothing about it, so it must not be judged against it.
func TestRefuseUnknownBuiltins_IgnoresReferencesThatAreNotBuiltins(t *testing.T) {
	t.Parallel()

	known := []string{"builtin.coherence"}

	assert.NoError(t, refuseUnknownBuiltins(
		[]string{"support-agent-quality", "my_eval.v2", "quality"}, known))
}

// The service returns built-ins prefixed today. Matching both spellings means a
// change of convention cannot start refusing every valid reference.
func TestRefuseUnknownBuiltins_MatchesEitherSpelling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		known []string
	}{
		{name: "catalogue returns prefixed names", known: []string{"builtin.coherence"}},
		{name: "catalogue returns bare names", known: []string{"coherence"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.NoError(t, refuseUnknownBuiltins(
				[]string{evalcore.BuiltinPrefix + "coherence"}, tt.known))
		})
	}
}

// Every reference is checked, not only the first.
func TestRefuseUnknownBuiltins_ChecksEveryReference(t *testing.T) {
	t.Parallel()

	err := refuseUnknownBuiltins(
		[]string{"builtin.coherence", "a-rubric", "builtin.nope"},
		[]string{"builtin.coherence"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "builtin.nope")
}

// With no azd server to reach, the listing cannot happen and the caller has to
// get the offline answer rather than an error or a hang.
func TestKnownBuiltinEvaluators_UnreachableProjectKnowsNothing(t *testing.T) {
	t.Setenv("AZD_SERVER", "")

	start := time.Now()
	known := knownBuiltinEvaluators(t.Context())

	assert.Empty(t, known, "nothing is known, so nothing can be refused")
	assert.Less(t, time.Since(start), builtinCatalogueTimeout,
		"an unreachable project fails fast rather than waiting out the bound")
}

// A caller whose context is already done must not be left waiting either.
func TestKnownBuiltinEvaluators_CancelledContextKnowsNothing(t *testing.T) {
	t.Setenv("AZD_SERVER", "")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	assert.Empty(t, knownBuiltinEvaluators(ctx))
}
