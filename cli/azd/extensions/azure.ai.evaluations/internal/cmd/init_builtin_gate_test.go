// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaieval/internal/pkg/evalcore"

	"github.com/stretchr/testify/assert"
)

// The catalogue lookup authenticates and can wait out its own timeout, and it
// can only answer about built-ins. A run that names none -- which is most of
// them, `init` having no --evaluator at all -- was paying for a connection
// that could not validate anything, on the one command whose value is that it
// works offline.
func TestHasBuiltinRef(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		refs []string
		want bool
	}{
		{"no references at all", nil, false},
		{"an empty list", []string{}, false},
		{"only custom references", []string{"support-quality", "tone-check"}, false},
		{"one built-in", []string{evalcore.BuiltinPrefix + "coherence"}, true},
		{
			"a built-in among custom ones",
			[]string{"support-quality", evalcore.BuiltinPrefix + "groundedness", "tone-check"},
			true,
		},
		{
			"a custom name that merely mentions the word",
			[]string{"my-builtin-style-rubric"},
			false,
		},
		{"an empty reference", []string{""}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, hasBuiltinRef(tt.refs))
		})
	}
}

// The gate has to match what the catalogue check consumes, or it skips a
// lookup that would have refused something. Every reference the shape check
// treats as a built-in is one hasBuiltinRef reports.
func TestHasBuiltinRefAgreesWithWhatTheCatalogueCheckLooksAt(t *testing.T) {
	t.Parallel()

	// A catalogue that answered, and does not offer this name. An empty
	// catalogue means "nothing could be reached" and deliberately refuses
	// nothing, so it cannot be used to show the gate matters.
	catalogue := []string{
		evalcore.BuiltinPrefix + "coherence",
		evalcore.BuiltinPrefix + "groundedness",
	}

	unknown := []string{evalcore.BuiltinPrefix + "does_not_exist"}
	assert.True(t, hasBuiltinRef(unknown), "the gate lets the ask through")
	assert.Error(t, refuseUnknownBuiltins(unknown, catalogue),
		"and the catalogue then refuses it, which is the whole point of asking")

	offered := []string{evalcore.BuiltinPrefix + "coherence"}
	assert.True(t, hasBuiltinRef(offered))
	assert.NoError(t, refuseUnknownBuiltins(offered, catalogue))

	// A custom reference the gate skips is one the check would pass anyway,
	// so skipping the lookup costs nothing.
	custom := []string{"support-quality"}
	assert.False(t, hasBuiltinRef(custom))
	assert.NoError(t, refuseUnknownBuiltins(custom, catalogue),
		"a custom reference is not the catalogue's to answer for")
}

// An unreachable project answers nothing, and nothing is not a refusal. This
// is what keeps the check from turning a working offline `init` into a
// failure, and it is the reason the gate is about cost rather than
// correctness.
func TestAnUnreachableCatalogueRefusesNothing(t *testing.T) {
	t.Parallel()

	refs := []string{evalcore.BuiltinPrefix + "does_not_exist"}
	assert.NoError(t, refuseUnknownBuiltins(refs, nil))
	assert.NoError(t, refuseUnknownBuiltins(refs, []string{}))
}
