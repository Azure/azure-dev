// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An eval pinned to an explicit `id:` is reserved before anything is decided.
//
// It used to be claimed only when its own declaration was reached, so file
// order decided the outcome: a declaration listed above it that resolved to the
// same eval through digestIDKey got there first, and the two ended up sharing
// one eval and one run history. That is precisely the collision ReserveDeclared
// exists to prevent, and the pinned declaration -- the one that named the eval
// outright -- was the one that lost.
func TestExplicitIDsAreReserved(t *testing.T) {
	r := &evalReconciler{}

	r.reserveExplicitIDs([]project.Eval{
		{Name: "nightly", ID: "eval_pinned"},
		{Name: "smoke"},
	})

	require.NotNil(t, r.claimed, "nothing was reserved at all")
	assert.True(t, r.claimed["eval_pinned"],
		"an id the author wrote down must be spoken for before any adoption runs")
	assert.False(t, r.claimed["smoke"],
		"a declaration with no id reserves nothing, which is what leaves a "+
			"genuine rename free to adopt")
}

// The reservation consults nothing, which is the property that makes it a
// reservation: the loop that follows it reads the recorded environment, and an
// entry whose read fails is skipped. A pinned id must not be skippable.
func TestExplicitIDReservationReadsNothing(t *testing.T) {
	// A zero reconciler has no eval context at all, so reaching for one would
	// panic. This passing is the proof that it does not.
	r := &evalReconciler{}

	r.reserveExplicitIDs([]project.Eval{{Name: "nightly", ID: "eval_pinned"}})

	assert.True(t, r.claimed["eval_pinned"])
}
