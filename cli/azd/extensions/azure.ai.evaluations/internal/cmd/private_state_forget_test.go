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

// A delete has to take its bookkeeping with it.
//
// The state is not shown anywhere, so nothing said the id and fingerprint of a
// deleted resource were still on file. The next deploy read them, bound a new
// eval of the same name to an id the service no longer has, and failed on a
// resource the reader had just recreated.
func TestForgetDropsTheMappingsOfADeletedResource(t *testing.T) {
	env := &testEnvServer{}
	ec := &evalContext{azdClient: newTestAzdClient(t, env), envName: "test"}
	ctx := context.Background()

	require.NoError(t, ec.setPrivate(ctx, idKey("eval", "nightly"), "evalgroup_1"))
	require.NoError(t, ec.setPrivate(ctx, project.FingerprintKey("eval", "nightly"), "abc"))
	require.NoError(t, ec.setPrivate(ctx, idKey("eval", "smoke"), "evalgroup_2"))

	ec.forget(ctx, idKey("eval", "nightly"), project.FingerprintKey("eval", "nightly"))

	assert.Empty(t, ec.privateValue(ctx, idKey("eval", "nightly")))
	assert.Empty(t, ec.privateValue(ctx, project.FingerprintKey("eval", "nightly")))
	assert.Equal(t, "evalgroup_2", ec.privateValue(ctx, idKey("eval", "smoke")),
		"the section is rewritten whole, so a delete must not take its neighbours")

	// And it is gone from what is persisted, not only from this command's copy.
	fresh := &evalContext{azdClient: newTestAzdClient(t, env), envName: "test"}
	assert.Empty(t, fresh.privateValue(ctx, idKey("eval", "nightly")))
	assert.Equal(t, "evalgroup_2", fresh.privateValue(ctx, idKey("eval", "smoke")))
}

// A dataset carries many versions and the state describes one of them, so
// removing an older version must not report the current one as unpublished --
// which would publish it again, as a third version.
func TestForgetDeletedVersionOnlyDropsTheRecordedOne(t *testing.T) {
	env := &testEnvServer{}
	ec := &evalContext{azdClient: newTestAzdClient(t, env), envName: "test"}
	ctx := context.Background()

	require.NoError(t, ec.setPrivate(ctx, versionKey("dataset", "golden"), "3.0"))
	require.NoError(t, ec.setPrivate(ctx, project.FingerprintKey("dataset", "golden"), "digest"))

	ec.forgetDeletedVersion(ctx, "dataset", "golden", "1.0")
	assert.Equal(t, "3.0", ec.privateValue(ctx, versionKey("dataset", "golden")),
		"an older version being removed says nothing about the recorded one")
	assert.Equal(t, "digest", ec.privateValue(ctx, project.FingerprintKey("dataset", "golden")))

	ec.forgetDeletedVersion(ctx, "dataset", "golden", "3.0")
	assert.Empty(t, ec.privateValue(ctx, versionKey("dataset", "golden")))
	assert.Empty(t, ec.privateValue(ctx, project.FingerprintKey("dataset", "golden")),
		"the fingerprint described content the service no longer has")
}

// Nothing recorded is nothing to drop, and a delete that succeeded remotely is
// not undone by an environment that could not be written.
func TestForgetIsQuietWithNothingToDrop(t *testing.T) {
	env := &testEnvServer{}
	ec := &evalContext{azdClient: newTestAzdClient(t, env), envName: "test"}
	ctx := context.Background()

	require.NoError(t, ec.deletePrivate(ctx, idKey("eval", "never-recorded")))

	// No azd environment at all is the ordinary case for a direct invocation.
	assert.NotPanics(t, func() {
		(&evalContext{}).forget(ctx, idKey("eval", "nightly"))
	})
}
