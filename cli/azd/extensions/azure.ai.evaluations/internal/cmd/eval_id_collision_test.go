// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// renamedThenReintroduced is the state a rename leaves behind: the id is
// recorded under the new name, and the old name's entry still points at it
// because nothing removes it.
func renamedThenReintroduced(t *testing.T) (map[string]string, project.Eval, project.Eval) {
	t.Helper()

	original := windowedEval("nightly", 24)
	digest, err := project.FingerprintGroup(original)
	require.NoError(t, err)

	renamed := original
	renamed.Name = "evening"

	return map[string]string{
		digestIDKey(digest):      "eval_1",
		idKey("eval", "evening"): "eval_1",
		// Left behind by the rename.
		idKey("eval", "nightly"): "eval_1",
	}, renamed, original
}

// A rename records the id under the new name and leaves the old name's entry
// pointing at the same eval. Reintroducing that old name with the same
// definition then found a live id cached under it and took it -- and unlike
// adoption, that path never asked whether another declaration already owned it.
//
// Both declarations resolved to one eval. Each deploy renamed it past the
// other, and the two shared a single run history while reading as two evals in
// the file.
func TestAReintroducedNameDoesNotTakeTheEvalTheRenameOwns(t *testing.T) {
	state, renamed, reintroduced := renamedThenReintroduced(t)
	env := &testEnvServer{state: state}
	r, created := evalServiceReconciler(t, env, "eval_1")

	groups := []project.Eval{renamed, reintroduced}
	r.ReserveDeclared(context.Background(), groups)

	first, _, err := r.EnsureEval(context.Background(), renamed, "")
	require.NoError(t, err)
	second, secondCreated, err := r.EnsureEval(context.Background(), reintroduced, "")
	require.NoError(t, err)

	assert.NotEqual(t, first, second,
		"two declarations must not resolve to one eval and one run history")
	assert.Equal(t, "eval_1", first, "the declaration that owns it keeps its runs")
	assert.True(t, secondCreated)
	assert.Len(t, *created, 1, "the other one gets an eval of its own")
}

// The same, with the file listing them the other way round. Which declaration
// keeps the eval follows declaration order -- nothing recorded locally says
// which of two identical definitions owned it first -- but they must never
// share one, whichever order they are in.
func TestTheCollisionIsRefusedInEitherOrder(t *testing.T) {
	state, renamed, reintroduced := renamedThenReintroduced(t)
	env := &testEnvServer{state: state}
	r, created := evalServiceReconciler(t, env, "eval_1")

	groups := []project.Eval{reintroduced, renamed}
	r.ReserveDeclared(context.Background(), groups)

	first, _, err := r.EnsureEval(context.Background(), reintroduced, "")
	require.NoError(t, err)
	second, _, err := r.EnsureEval(context.Background(), renamed, "")
	require.NoError(t, err)

	assert.NotEqual(t, first, second)
	assert.Len(t, *created, 1)
}

// Once the collision has been settled the state is no longer ambiguous: the
// declaration that created its own eval has recorded that id under its name, so
// the stale entry is gone and the next deploy reuses both without a tiebreak.
func TestTheCollisionSettlesAfterOneDeploy(t *testing.T) {
	state, renamed, reintroduced := renamedThenReintroduced(t)
	env := &testEnvServer{state: state}
	r, _ := evalServiceReconciler(t, env, "eval_1")

	groups := []project.Eval{renamed, reintroduced}
	r.ReserveDeclared(context.Background(), groups)
	_, _, err := r.EnsureEval(context.Background(), renamed, "")
	require.NoError(t, err)
	_, _, err = r.EnsureEval(context.Background(), reintroduced, "")
	require.NoError(t, err)

	assert.NotEqual(t, "eval_1", env.stored(t, idKey("eval", "nightly")),
		"the entry the rename left behind has been replaced by this "+
			"declaration's own eval, so the ambiguity does not recur")
}

// The ordinary case has to keep working: one declaration, its own recorded id,
// reserved by itself. "Already claimed" is what reuse looks like when it is
// working, so the guard must key on who claimed it rather than whether anyone
// did.
func TestADeclarationStillReusesTheEvalItOwns(t *testing.T) {
	group := windowedEval("nightly", 24)
	env := &testEnvServer{state: map[string]string{
		idKey("eval", "nightly"): "eval_1",
	}}
	r, created := evalServiceReconciler(t, env, "eval_1")

	r.ReserveDeclared(context.Background(), []project.Eval{group})
	id, wasCreated, err := r.EnsureEval(context.Background(), group, "")

	require.NoError(t, err)
	assert.Equal(t, "eval_1", id, "its own reservation must not lock it out")
	assert.False(t, wasCreated)
	assert.Empty(t, *created)
}
