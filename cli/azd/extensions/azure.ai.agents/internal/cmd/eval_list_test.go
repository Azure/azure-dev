// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// newEvalListCommand — command shape
// ---------------------------------------------------------------------------

func TestNewEvalListCommand_Flags(t *testing.T) {
	t.Parallel()
	cmd := newEvalListCommand()

	f := cmd.Flags().Lookup("limit")
	require.NotNil(t, f)
	assert.Equal(t, "10", f.DefValue)
}

func TestNewEvalListCommand_NoArgs(t *testing.T) {
	t.Parallel()
	cmd := newEvalListCommand()
	assert.NoError(t, cmd.Args(cmd, nil))
	assert.Error(t, cmd.Args(cmd, []string{"extra"}))
}

func TestNewEvalListCommand_UseString(t *testing.T) {
	t.Parallel()
	cmd := newEvalListCommand()
	assert.Equal(t, "list", cmd.Use)
}
