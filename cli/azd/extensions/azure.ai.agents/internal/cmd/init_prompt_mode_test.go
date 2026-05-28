// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// chdirTo is a small t.Helper that runs the test in a fresh empty dir.
// t.Chdir restores cwd at the end of the test.
func chdirTo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

// TestPromptInitMode_FromCodeFlagWins covers routing rule #1: an
// explicit --from-code flag short-circuits everything. The dir is
// non-empty, which would normally trigger the Select prompt; the flag
// must override that.
func TestPromptInitMode_FromCodeFlagWins(t *testing.T) {
	dir := chdirTo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.py"), []byte("x\n"), 0o644)) //nolint:gosec

	// nil azdClient is safe here because the from-code short-circuit
	// returns before any Prompt RPC is attempted.
	mode, err := promptInitMode(context.Background(), nil, &initFlags{fromCode: true})
	require.NoError(t, err)
	assert.Equal(t, initModeFromCode, mode)
}

// TestPromptInitMode_EmptyDirSelectsTemplate covers routing rule #2.
// This is the legacy behavior preserved for backwards-compatibility:
// no code => offer templates.
func TestPromptInitMode_EmptyDirSelectsTemplate(t *testing.T) {
	_ = chdirTo(t)

	mode, err := promptInitMode(context.Background(), nil, &initFlags{})
	require.NoError(t, err)
	assert.Equal(t, initModeTemplate, mode)
}

// TestPromptInitMode_NonEmptyNoPromptDefaultsToFromCode covers routing
// rule #3 -- the path coding agents land on when they call
// `azd ai agent init --no-prompt` without `-m <url>` or `--from-code`.
// In non-interactive mode with existing local files, we default to using
// the current directory (from-code) rather than erroring.
func TestPromptInitMode_NonEmptyNoPromptDefaultsToFromCode(t *testing.T) {
	dir := chdirTo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.py"), []byte("x\n"), 0o644)) //nolint:gosec

	mode, err := promptInitMode(context.Background(), nil, &initFlags{noPrompt: true})
	require.NoError(t, err)
	assert.Equal(t, initModeFromCode, mode)
}
