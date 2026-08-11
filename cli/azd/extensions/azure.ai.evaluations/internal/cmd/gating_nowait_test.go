// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `--no-wait --fail-on ...` reads as "start it and tell me if it regressed".
// It cannot be: --no-wait returns before there is a result, so the gate was
// dropped and the command exited 0 however the run turned out. A pipeline
// written that way believes it is gated and is not, which is worse than not
// gating at all.
//
// Refused up front, before any network work.
func TestFailOnWithNoWaitIsRefused(t *testing.T) {
	for _, gate := range []string{"any-failure", "pass-rate=0.8"} {
		root := NewRootCommand()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs([]string{"run", "start", "--no-wait", "--fail-on", gate})

		err := root.ExecuteContext(context.Background())

		require.Errorf(t, err, "--no-wait with --fail-on %s must not be accepted", gate)
		assert.Contains(t, err.Error(), "--fail-on")
		assert.Contains(t, err.Error(), "--no-wait")
		assert.Containsf(t, err.Error(), "run show",
			"the refusal has to name the way to gate a run started with --no-wait")
	}
}

// The gate on its own still parses and still reaches the run, so the refusal
// above is about the combination and not about --fail-on.
func TestFailOnAloneIsStillAccepted(t *testing.T) {
	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"run", "start", "--fail-on", "pass-rate=0.8"})

	err := root.ExecuteContext(context.Background())

	if err != nil {
		assert.NotContains(t, err.Error(), "--no-wait",
			"a gate without --no-wait must not be refused for needing the wait")
	}
}
