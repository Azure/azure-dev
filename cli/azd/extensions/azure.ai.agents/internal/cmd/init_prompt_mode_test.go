// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
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
	mode, err := promptInitMode(context.Background(), nil, &initFlags{fromCode: true}, &bytes.Buffer{})
	require.NoError(t, err)
	assert.Equal(t, initModeFromCode, mode)
}

// TestPromptInitMode_EmptyDirSelectsTemplate covers routing rule #2.
// This is the legacy behavior preserved for backwards-compatibility:
// no code => offer templates.
func TestPromptInitMode_EmptyDirSelectsTemplate(t *testing.T) {
	_ = chdirTo(t)

	mode, err := promptInitMode(context.Background(), nil, &initFlags{}, &bytes.Buffer{})
	require.NoError(t, err)
	assert.Equal(t, initModeTemplate, mode)
}

// TestPromptInitMode_NonEmptyNoPromptReturnsSuggestion covers routing
// rule #3 -- the path coding agents land on when they call
// `azd ai agent init --no-prompt` without `-m <url>` or `--from-code`.
// Rather than letting the Select RPC fail opaquely, we return an
// ErrorWithSuggestion the coding agent can act on.
func TestPromptInitMode_NonEmptyNoPromptReturnsSuggestion(t *testing.T) {
	dir := chdirTo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.py"), []byte("x\n"), 0o644)) //nolint:gosec

	_, err := promptInitMode(context.Background(), nil, &initFlags{noPrompt: true}, &bytes.Buffer{})
	require.Error(t, err)

	// The suggestion should name BOTH escape hatches so the caller
	// (often a coding agent) can pick the right one. LocalError.Error()
	// returns only Message, so we assert on the Suggestion field
	// directly via the structured error.
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected *azdext.LocalError, got %T: %v", err, err)

	assert.Contains(t, localErr.Suggestion, "--from-code",
		"suggestion should mention --from-code as the 'use existing code' escape hatch")
	assert.Contains(t, localErr.Suggestion, "--manifest",
		"suggestion should mention --manifest as the 'pick an agent template' escape hatch")

	assert.Equal(t, exterrors.CodePromptFailed, localErr.Code,
		"non-interactive init-mode failure should be tagged with CodePromptFailed for telemetry")
	assert.Equal(t, azdext.LocalErrorCategoryValidation, localErr.Category,
		"the failure is a user-input issue, not a dependency or auth failure")
}
