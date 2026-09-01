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

// Reconciliation state is the extension's own bookkeeping, not configuration.
//
// It used to be written as ordinary azd environment values, so `azd env
// get-values` handed a reader content fingerprints, rename indexes and
// per-object version caches alongside their own settings, and every hook
// received them. Nobody sets these by hand; they exist so an immutable artifact
// is not republished. One environment had accumulated sixteen of them, six
// still pointing at resources that had been deleted.
func TestPrivateStateStaysOutOfEnvironmentValues(t *testing.T) {
	env := &testEnvServer{}
	ec := &evalContext{azdClient: newTestAzdClient(t, env), envName: "test"}
	ctx := context.Background()

	for _, key := range []string{
		project.FingerprintKey("dataset", "golden"),
		versionKey("dataset", "golden"),
		idKey("eval", "nightly"),
		idKey("evalrun", "eval_1"),
		digestIDKey("607f24013a81d1234567890abcdef012"),
	} {
		require.NoError(t, ec.setPrivate(ctx, key, "recorded"))
	}

	assert.Empty(t, env.values,
		"nothing private belongs in the values a user reads back:\n%v", env.values)
	require.NotEmpty(t, env.config[privateStatePath],
		"the state has to actually be somewhere")

	// And it reads back, because the point of recording it is not republishing.
	fresh := &evalContext{azdClient: newTestAzdClient(t, env), envName: "test"}
	assert.Equal(t, "recorded", fresh.privateValue(ctx, idKey("eval", "nightly")))
}

// A write that fails leaves nothing behind that a later read in the same
// command could mistake for stored state.
func TestAFailedWriteIsNotRememberedInMemory(t *testing.T) {
	env := &testEnvServer{failSetConfig: true}
	ec := &evalContext{azdClient: newTestAzdClient(t, env), envName: "test"}
	ctx := context.Background()

	require.Error(t, ec.setPrivate(ctx, idKey("eval", "nightly"), "evalgroup_1"))
	assert.Empty(t, ec.privateValue(ctx, idKey("eval", "nightly")),
		"a value that never reached azd must not read back as though it had")
}

// The extension writes no azd environment values at all.
//
// EVAL_ID, EVAL_RUN_ID and EVAL_DATASET_VERSION were written by every deploy
// and read by nothing -- not this extension, not the project package, whose
// exported constants for them were referenced nowhere. They accumulated: one
// environment held globals still naming an eval and a run that had been
// deleted, and a reader could not tell which declaration either belonged to.
// The per-eval entries carry the same facts and say whose they are.
func TestNoGlobalIdsAreWrittenToTheEnvironment(t *testing.T) {
	env := &testEnvServer{}
	ec := &evalContext{azdClient: newTestAzdClient(t, env), envName: "test"}
	ctx := context.Background()

	ec.remember(ctx, idKey("eval", "nightly"), "evalgroup_1")
	ec.remember(ctx, idKey("evalrun", "evalgroup_1"), "evalrun_1")
	ec.remember(ctx, versionKey("dataset", "golden"), "1.0")

	for _, gone := range []string{"EVAL_ID", "EVAL_RUN_ID", "EVAL_DATASET_VERSION"} {
		assert.NotContainsf(t, env.values, gone,
			"%s is written by every deploy and read by nothing", gone)
	}
	assert.Empty(t, env.values, "nothing belongs in the environment values")
}
