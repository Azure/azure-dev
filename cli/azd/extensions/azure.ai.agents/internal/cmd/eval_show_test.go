// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- newEvalShowCommand ----

func TestNewEvalShowCommand_UseString(t *testing.T) {
	t.Parallel()
	cmd := newEvalShowCommand(&azdext.ExtensionContext{})
	assert.Equal(t, "show [eval-id]", cmd.Use)
}

func TestNewEvalShowCommand_Flags(t *testing.T) {
	t.Parallel()
	cmd := newEvalShowCommand(&azdext.ExtensionContext{})

	tests := []struct {
		name     string
		flag     string
		wantNil  bool
		defValue string
	}{
		{"eval-run-id flag", "eval-run-id", false, ""},
		{"limit flag", "limit", false, "20"},
		{"out-file flag", "out-file", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := cmd.Flags().Lookup(tt.flag)
			if tt.wantNil {
				assert.Nil(t, f)
			} else {
				require.NotNil(t, f, "--%s flag should be registered", tt.flag)
				assert.Equal(t, tt.defValue, f.DefValue)
			}
		})
	}
}

func TestNewEvalShowCommand_AcceptsOptionalPositionalArg(t *testing.T) {
	t.Parallel()
	cmd := newEvalShowCommand(&azdext.ExtensionContext{})
	// MaximumNArgs(1) — should accept 0 args without error from arg validation.
	assert.NotNil(t, cmd.Args)
}

func TestNewEvalShowCommand_HasOutFileShorthand(t *testing.T) {
	t.Parallel()
	cmd := newEvalShowCommand(&azdext.ExtensionContext{})
	f := cmd.Flags().Lookup("out-file")
	require.NotNil(t, f)
	assert.Equal(t, "O", f.Shorthand)
}

// ---- newEvalUpdateCommand ----

func TestNewEvalUpdateCommand_UseString(t *testing.T) {
	t.Parallel()
	cmd := newEvalUpdateCommand(&azdext.ExtensionContext{})
	assert.Equal(t, "update", cmd.Use)
}

func TestNewEvalUpdateCommand_Flags(t *testing.T) {
	t.Parallel()
	cmd := newEvalUpdateCommand(&azdext.ExtensionContext{})

	tests := []struct {
		name     string
		flag     string
		defValue string
	}{
		{"config flag", "config", defaultEvalConfigName},
		{"dataset-only flag", "dataset-only", "false"},
		{"evaluator-only flag", "evaluator-only", "false"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := cmd.Flags().Lookup(tt.flag)
			require.NotNil(t, f, "--%s flag should be registered", tt.flag)
			assert.Equal(t, tt.defValue, f.DefValue)
		})
	}
}

func TestNewEvalUpdateCommand_NoArgs(t *testing.T) {
	t.Parallel()
	cmd := newEvalUpdateCommand(&azdext.ExtensionContext{})
	assert.NotNil(t, cmd.Args)
}

// ---- eval "update" in parent command ----

func TestNewEvalCommand_HasUpdateSubcommand(t *testing.T) {
	t.Parallel()
	cmd := newEvalCommand(&azdext.ExtensionContext{})
	var names []string
	for _, sub := range cmd.Commands() {
		names = append(names, sub.Name())
	}
	assert.Contains(t, names, "update")
}

// ---- eval "show" in parent command ----

func TestNewEvalCommand_HasShowSubcommand(t *testing.T) {
	t.Parallel()
	cmd := newEvalCommand(&azdext.ExtensionContext{})
	var names []string
	for _, sub := range cmd.Commands() {
		names = append(names, sub.Name())
	}
	assert.Contains(t, names, "show")
}
