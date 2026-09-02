// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A read that failed used to be indistinguishable from a store that was empty.
//
// loadPrivateState cached the empty map either way, and setPrivate replaces the
// whole section -- so the next recorded id was written over a baseline nobody
// had read, deleting every other id and fingerprint in it. Silently, and only
// on the runs where the read happened to fail, which is the worst way for it to
// happen: the deploy after it republishes immutable versions and looks like a
// first deployment.
func TestAStateReadThatFailedIsNotWrittenOver(t *testing.T) {
	env := &testEnvServer{
		state: map[string]string{
			"eval:support-agent":    "eval_existing",
			"dataset:golden:v":      "3.0",
			"fingerprint:evaluator": "abc123",
		},
		failGetConfig: true,
	}
	ec := &evalContext{azdClient: newTestAzdClient(t, env), envName: "test"}

	err := ec.setPrivate(t.Context(), "eval:another", "eval_new")

	require.Error(t, err, "a write over an unread section has to be refused")
	assert.Contains(t, err.Error(), "could not be read")

	// The decisive part: nothing was persisted, so what the section already
	// held is still there for the next command to read.
	assert.Nil(t, env.config[privateStatePath],
		"nothing may be written when the baseline is unknown")
	assert.Equal(t, "eval_existing", env.stored(t, "eval:support-agent"))
	assert.Equal(t, "3.0", env.stored(t, "dataset:golden:v"))
}

// The ordinary path is unchanged: a readable store still records, and a store
// that is genuinely empty is not a failure.
func TestAReadableStateStillRecords(t *testing.T) {
	env := &testEnvServer{state: map[string]string{"eval:existing": "eval_1"}}
	ec := &evalContext{azdClient: newTestAzdClient(t, env), envName: "test"}

	require.NoError(t, ec.setPrivate(t.Context(), "eval:added", "eval_2"))

	assert.Equal(t, "eval_2", env.stored(t, "eval:added"))
	assert.Equal(t, "eval_1", env.stored(t, "eval:existing"),
		"recording one key must not drop the others")
}
