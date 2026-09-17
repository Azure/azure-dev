// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	scopeA = "evals/azure.eval.yaml"
	scopeB = "support/evals/azure.eval.yaml"
)

// reader is a command that has not read the state yet, which is what every
// later deploy is: each caches what it read, so reusing one would hide a write.
func reader(t *testing.T, env *testEnvServer) *evalContext {
	t.Helper()
	return &evalContext{azdClient: newTestAzdClient(t, env), envName: "test"}
}

// An environment written before scoping existed holds ids under the unqualified
// key. Reading them from the new code has to find them: the alternative is
// creating a second eval and leaving the first -- with every run ever made
// against it -- reachable only by an id nobody recorded.
func TestAnIDRecordedBeforeScopingIsStillFound(t *testing.T) {
	base := idKey("eval", "quality")
	env := &testEnvServer{state: map[string]string{base: "evalgroup_existing"}}

	assert.Equal(t, "evalgroup_existing",
		reader(t, env).scopedValue(context.Background(), base, scopeA),
		"an upgrade must not strand the id it already had")
}

// The first configuration to record an id keeps the unqualified key, so the
// next deploy of that same configuration reads exactly what it wrote.
func TestTheFirstConfigurationKeepsTheUnqualifiedKey(t *testing.T) {
	base := idKey("eval", "quality")
	env := &testEnvServer{}
	ctx := context.Background()

	reader(t, env).rememberScoped(ctx, base, scopeA, "evalgroup_a")

	after := reader(t, env)
	assert.Equal(t, "evalgroup_a", after.privateValue(ctx, base),
		"the unqualified key is the one an existing project already reads")
	assert.Equal(t, scopeA, after.privateValue(ctx, base+project.EvalScopeSuffix),
		"and it is now on record whose it is")
	assert.Equal(t, "evalgroup_a", reader(t, env).scopedValue(ctx, base, scopeA))
}

// The collision this exists to stop: a second configuration declaring the same
// eval name must not be handed the first one's eval.
func TestASecondConfigurationDoesNotInheritTheFirstsEval(t *testing.T) {
	base := idKey("eval", "quality")
	env := &testEnvServer{}
	ctx := context.Background()

	reader(t, env).rememberScoped(ctx, base, scopeA, "evalgroup_a")

	assert.Empty(t, reader(t, env).scopedValue(ctx, base, scopeB),
		"a different configuration has recorded nothing yet, whatever the name")

	reader(t, env).rememberScoped(ctx, base, scopeB, "evalgroup_b")

	assert.Equal(t, "evalgroup_a", reader(t, env).privateValue(ctx, base),
		"the first configuration's id is left exactly where it was")
	assert.Equal(t, "evalgroup_a", reader(t, env).scopedValue(ctx, base, scopeA))
	assert.Equal(t, "evalgroup_b", reader(t, env).scopedValue(ctx, base, scopeB))
}

// Ownership is recorded once and never moved, so ids do not swap between
// configurations depending on which deploy ran last.
func TestOwnershipOfTheUnqualifiedKeyDoesNotMove(t *testing.T) {
	base := idKey("eval", "quality")
	env := &testEnvServer{}
	ctx := context.Background()

	reader(t, env).rememberScoped(ctx, base, scopeA, "evalgroup_a")

	for range 3 {
		reader(t, env).rememberScoped(ctx, base, scopeB, "evalgroup_b")
		check := reader(t, env)
		assert.Equal(t, scopeA, check.privateValue(ctx, base+project.EvalScopeSuffix),
			"the owner is whoever got there first, on every later deploy too")
		assert.Equal(t, "evalgroup_a", check.privateValue(ctx, base))
		assert.Equal(t, "evalgroup_b", check.scopedValue(ctx, base, scopeB))
	}
}

// Without a scope nothing changes. That is the path before a configuration is
// known, and every environment written before this existed.
func TestNoScopeReadsAndWritesTheUnqualifiedKey(t *testing.T) {
	base := idKey("eval", "quality")
	env := &testEnvServer{}
	ctx := context.Background()

	reader(t, env).rememberScoped(ctx, base, "", "evalgroup_plain")

	after := reader(t, env)
	assert.Equal(t, "evalgroup_plain", after.privateValue(ctx, base))
	assert.Empty(t, after.privateValue(ctx, base+project.EvalScopeSuffix),
		"nothing to record when there is no configuration to name")
	assert.Equal(t, "evalgroup_plain", reader(t, env).scopedValue(ctx, base, ""))
}

// The upgrade path: a scope arriving after an unscoped deploy adopts what that
// deploy left, rather than starting a second eval beside it.
func TestAScopeAdoptsWhatAnUnscopedDeployLeft(t *testing.T) {
	base := idKey("eval", "quality")
	env := &testEnvServer{}
	ctx := context.Background()

	reader(t, env).rememberScoped(ctx, base, "", "evalgroup_plain")
	assert.Equal(t, "evalgroup_plain", reader(t, env).scopedValue(ctx, base, scopeA),
		"an unowned key belongs to whichever configuration asks first")

	reader(t, env).rememberScoped(ctx, base, scopeA, "evalgroup_plain")
	assert.Equal(t, "evalgroup_plain", reader(t, env).privateValue(ctx, base),
		"and the id it points at is unchanged")
}

// Two scopes must not land on the same key, or the fix reintroduces the bug.
func TestTwoScopesProduceDifferentKeys(t *testing.T) {
	base := idKey("eval", "quality")
	env := &testEnvServer{}
	ctx := context.Background()

	reader(t, env).rememberScoped(ctx, base, scopeA, "evalgroup_a")
	keyB := reader(t, env).scopedKey(ctx, base, scopeB)

	require.NotEqual(t, base, keyB, "the second configuration needs a key of its own")
	assert.Contains(t, keyB, project.EvalScopeTag(scopeB),
		"named for the configuration it belongs to")
}

// The scope is computed on both sides of the deploy/lookup split, so it has to
// survive the two ways the same path gets spelled.
//
// Built from t.TempDir rather than a `C:\proj` literal: on Linux that literal
// is an ordinary relative filename, so filepath.Rel answers `../C:\proj\...`
// and the test would exercise nothing while appearing to pass or fail for the
// wrong reason.
func TestTheScopeIsTheSameOnBothSidesOfThePath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	config := filepath.Join(root, "evals", "azure.eval.yaml")

	assert.Equal(t, "evals/azure.eval.yaml", project.EvalScope(root, config),
		"relative to the root, forward slashed")

	assert.NotEqual(t,
		project.EvalScope(root, config),
		project.EvalScope(root, filepath.Join(root, "support", "evals", "azure.eval.yaml")),
		"two configurations are two scopes")

	assert.Empty(t, project.EvalScope(root, ""), "nothing to identify")
}

// Case is the filesystem's business. Folding it everywhere made two files on
// Linux -- `evals/A/...` and `evals/a/...` -- one scope, so each would read and
// overwrite the other's recorded ids: the collision this whole mechanism exists
// to prevent, reintroduced by the normalization meant to help it.
func TestCaseIsFoldedOnlyWhereTheFilesystemFoldsIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	lower := project.EvalScope(root, filepath.Join(root, "evals", "a", "azure.eval.yaml"))
	upper := project.EvalScope(root, filepath.Join(root, "evals", "A", "azure.eval.yaml"))

	if runtime.GOOS == "windows" {
		assert.Equal(t, lower, upper,
			"one file spelled two ways is one configuration here")
		return
	}
	assert.NotEqual(t, lower, upper,
		"two files are two configurations, and must not share recorded ids")
}
