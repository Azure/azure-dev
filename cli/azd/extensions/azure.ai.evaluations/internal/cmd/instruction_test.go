// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A useful generation instruction is often longer than fits on a command
// line, so it can come from a file instead.
func TestResolveInstructionReadsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "instruction.md")
	require.NoError(t, os.WriteFile(path,
		[]byte("  A customer support agent answering billing questions.\n\n"), 0o600))

	got, err := resolveInstruction("", path)
	require.NoError(t, err)
	require.Equal(t, "A customer support agent answering billing questions.", got,
		"surrounding whitespace should be trimmed")
}

func TestResolveInstructionPrefersInlineWhenNoFile(t *testing.T) {
	got, err := resolveInstruction("inline text", "")
	require.NoError(t, err)
	require.Equal(t, "inline text", got)

	got, err = resolveInstruction("", "")
	require.NoError(t, err)
	require.Empty(t, got)
}

// An unreadable or empty file is reported rather than silently generating from
// no instruction at all.
func TestResolveInstructionRejectsUnusableFile(t *testing.T) {
	_, err := resolveInstruction("", filepath.Join(t.TempDir(), "absent.md"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "agent-instruction-file")

	empty := filepath.Join(t.TempDir(), "empty.md")
	require.NoError(t, os.WriteFile(empty, []byte("   \n"), 0o600))
	_, err = resolveInstruction("", empty)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty")
}
