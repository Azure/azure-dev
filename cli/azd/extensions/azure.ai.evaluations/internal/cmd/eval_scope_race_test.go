// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/require"
)

// azd deploys services concurrently, and each deploy builds its own context over
// the same section with its own cached view of it. Choosing the key from that
// cached view let two first-time deploys both read "unowned", both write the
// unqualified key, and the second overwrite the first's id while the ownership
// marker still named the first -- one service left with no mapping, and its next
// deploy making a second eval.
//
// Two real contexts over one section, each having read it before either wrote:
// the interleaving, run in the order that loses the update.
func TestTwoScopesCannotBothClaimTheUnqualifiedKey(t *testing.T) {
	const base = "EVAL_ID_QUALITY"

	srv := &envScopedServer{}
	first := &evalContext{azdClient: newTestAzdClient(t, srv), envName: "staging"}
	second := &evalContext{azdClient: newTestAzdClient(t, srv), envName: "staging"}

	// Both take their view of the section before either has written to it.
	first.loadPrivateState(t.Context())
	second.loadPrivateState(t.Context())

	first.rememberScoped(t.Context(), base, "scope/a", "id-a")
	second.rememberScoped(t.Context(), base, "scope/b", "id-b")

	stored := map[string]string{}
	require.Contains(t, srv.sections, "staging")
	require.NoError(t, json.Unmarshal(srv.sections["staging"][privateStatePath], &stored))

	owner := stored[base+project.EvalScopeSuffix]
	require.NotEmpty(t, owner, "somebody has to own the unqualified key: %v", stored)

	// The id under the unqualified key has to be the owner's, and the other
	// scope has to keep a mapping of its own.
	switch owner {
	case "scope/a":
		require.Equal(t, "id-a", stored[base],
			"scope/a owns the unqualified key, so its id belongs there: %v", stored)
		require.Equal(t, "id-b", stored[base+"_"+project.EvalScopeTag("scope/b")],
			"scope/b lost its mapping: %v", stored)
	case "scope/b":
		require.Equal(t, "id-b", stored[base], "%v", stored)
		require.Equal(t, "id-a", stored[base+"_"+project.EvalScopeTag("scope/a")],
			"scope/a lost its mapping: %v", stored)
	default:
		t.Fatalf("unexpected owner %q in %v", owner, stored)
	}
}

// One scope keeps the unqualified key, which is what leaves an existing
// project's recorded ids reachable now that scoping exists.
func TestOneScopeKeepsTheUnqualifiedKey(t *testing.T) {
	const base = "EVAL_ID_QUALITY"

	srv := &envScopedServer{}
	ec := &evalContext{azdClient: newTestAzdClient(t, srv), envName: "staging"}

	ec.rememberScoped(t.Context(), base, "scope/a", "id-a")
	ec.rememberScoped(t.Context(), base, "scope/a", "id-a2")

	stored := map[string]string{}
	require.NoError(t, json.Unmarshal(srv.sections["staging"][privateStatePath], &stored))

	require.Equal(t, "id-a2", stored[base], "the owner keeps writing the plain key: %v", stored)
	require.Equal(t, "scope/a", stored[base+project.EvalScopeSuffix])
	require.NotContains(t, stored, base+"_"+project.EvalScopeTag("scope/a"),
		"the owner does not also get a qualified key: %v", stored)
}
