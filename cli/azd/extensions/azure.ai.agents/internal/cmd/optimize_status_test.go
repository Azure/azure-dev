// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOptimizeStatusCommand_AcceptsOptionalPositionalArg(t *testing.T) {
	cmd := newOptimizeStatusCommand()

	// Zero args is now OK (uses last job ID)
	err := cmd.Args(cmd, []string{})
	assert.NoError(t, err)

	// One arg is OK
	err = cmd.Args(cmd, []string{"opt_abc123"})
	assert.NoError(t, err)

	// Two args is rejected
	err = cmd.Args(cmd, []string{"opt_abc123", "extra"})
	assert.Error(t, err)
}

func TestOptimizeStatusCommand_HasWatchFlag(t *testing.T) {
	cmd := newOptimizeStatusCommand()

	f := cmd.Flags().Lookup("watch")
	require.NotNil(t, f, "--watch flag should be registered")

	watchVal, err := cmd.Flags().GetBool("watch")
	require.NoError(t, err)
	assert.False(t, watchVal, "--watch should default to false for status")
}
