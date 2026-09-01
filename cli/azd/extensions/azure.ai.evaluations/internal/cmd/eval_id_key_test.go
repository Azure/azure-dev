// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// EVAL_ID is written by every deploy, so nothing tells a value meant for this
// declaration from one left behind by the eval it replaced.
//
// Reading it as a fallback meant a file whose one entry had been swapped ran
// the previous eval's criteria over the new one's rows and reported success,
// and `run cancel` with no arguments cancelled a run of the eval the file no
// longer described -- a destructive verb on a resource picked by accident.
//
// This is the only enforcement of that. The reasoning lives in a comment on
// the reconciler and in another on `resolveEvalID`, and a comment cannot fail.
func TestRecordedEvalIDIgnoresTheSharedKey(t *testing.T) {
	env := &testEnvServer{state: map[string]string{
		envKeyEvalID: "evalgroup_the_one_this_replaced",
	}}
	ec := &evalContext{azdClient: newTestAzdClient(t, env), envName: "test"}

	assert.Empty(t, ec.recordedEvalID(context.Background(), "nightly"),
		"a shared key cannot say which declaration it belongs to")

	// The entry recorded under the eval's own name does answer, which is what
	// makes the miss above a deliberate refusal rather than a broken read. A
	// second context because the first cached the state it read.
	env.state[idKey("eval", "nightly")] = "evalgroup_nightly"
	fresh := &evalContext{azdClient: newTestAzdClient(t, env), envName: "test"}
	require.Equal(t, "evalgroup_nightly", fresh.recordedEvalID(context.Background(), "nightly"))
}
