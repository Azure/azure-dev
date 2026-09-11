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

// azd deploys services concurrently by default, and each service's deploy gets
// its own context holding its own copy of this section. So between one service
// reading the section and writing it back, a sibling can have added keys to it
// -- and the write replaces the whole section.
//
// Writing the view taken at the start of the command deletes them, and the loss
// only shows up on the next `azd up`, which reads those resources as untracked
// and publishes a second immutable version of each. The baseline has to be the
// section as it is at the moment of writing, not as it was at the first read.
func TestRecordingMergesIntoTheStateAsItIsNow(t *testing.T) {
	env := &testEnvServer{state: map[string]string{"dataset:golden:v": "3"}}
	ec := &evalContext{azdClient: newTestAzdClient(t, env), envName: "test"}

	// This command reads the section, which is what caches it.
	require.Equal(t, "3", ec.privateValue(t.Context(), "dataset:golden:v"))

	// A sibling service's deploy records a key of its own.
	env.state["eval:sibling"] = "eval_9"

	// And this command records one of its own afterwards.
	require.NoError(t, ec.setPrivate(t.Context(), "eval:mine", "eval_1"))

	assert.Equal(t, "eval_9", env.stored(t, "eval:sibling"),
		"a key added since this command's read has to survive its write")
	assert.Equal(t, "eval_1", env.stored(t, "eval:mine"))
	assert.Equal(t, "3", env.stored(t, "dataset:golden:v"))
}

// A failed re-read is the same refusal as a failed first read, for the same
// reason: the write replaces everything, so it must not go out over a baseline
// nobody could read.
func TestAStateRereadThatFailedIsNotWrittenOver(t *testing.T) {
	env := &testEnvServer{state: map[string]string{"eval:existing": "eval_1"}}
	ec := &evalContext{azdClient: newTestAzdClient(t, env), envName: "test"}

	// The first read succeeds, so the refusal below can only come from the one
	// taken at write time.
	require.Equal(t, "eval_1", ec.privateValue(t.Context(), "eval:existing"))
	env.failGetConfig = true

	err := ec.setPrivate(t.Context(), "eval:added", "eval_2")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not be read")
	assert.Nil(t, env.config[privateStatePath], "nothing may be written")
}
