// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `--force` is permission to replace the destination, not to remove whatever
// happens to sit beside it. The first cut moved the old folder to a fixed
// `<dest>.azd-replaced` and deleted anything already there, which is a path
// this command does not own.
func TestReplacingAFolderDoesNotTouchANeighbour(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "golden-1.0")
	require.NoError(t, os.MkdirAll(dest, 0o750))

	// Exactly the name the first cut would have reused.
	neighbour := dest + ".azd-replaced"
	require.NoError(t, os.WriteFile(neighbour, []byte("mine"), 0o600))

	staging, err := os.MkdirTemp(dir, ".azd-dataset-*")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(staging, "rows.jsonl"), []byte("{}\n"), 0o600))

	require.NoError(t, replaceDir(staging, dest, true))

	// #nosec G304 -- both paths are inside this test's own TempDir.
	body, err := os.ReadFile(neighbour)
	require.NoError(t, err, "the neighbouring file must still be there")
	assert.Equal(t, "mine", string(body))

	// #nosec G304 -- both paths are inside this test's own TempDir.
	rows, err := os.ReadFile(filepath.Join(dest, "rows.jsonl"))
	require.NoError(t, err, "and the download still landed")
	assert.Equal(t, "{}\n", string(rows))
}

// Nothing is left behind once the new directory is in place.
func TestReplacingAFolderLeavesNoHoldingDirectory(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "golden-1.0")
	require.NoError(t, os.MkdirAll(dest, 0o750))

	staging, err := os.MkdirTemp(dir, ".azd-dataset-*")
	require.NoError(t, err)

	require.NoError(t, replaceDir(staging, dest, true))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.False(t, strings.HasPrefix(e.Name(), ".azd-replaced"),
			"the holding name is discarded once the new directory is in place, got %q", e.Name())
	}
}

// The destination is checked before the download, but the files are fetched in
// between and that can take minutes. A directory appearing in the meantime was
// moved aside and deleted anyway, so a download nobody forced destroyed work
// the caller had just created.
func TestReplacingAFolderRefusesWhenNotForced(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "golden-1.0")
	require.NoError(t, os.MkdirAll(dest, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dest, "theirs.txt"), []byte("mine"), 0o600))

	staging, err := os.MkdirTemp(dir, ".azd-dataset-*")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(staging, "rows.jsonl"), []byte("{}\n"), 0o600))

	err = replaceDir(staging, dest, false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--force", "the refusal has to say how to go ahead")

	// #nosec G304 -- both paths are inside this test's own TempDir.
	body, readErr := os.ReadFile(filepath.Join(dest, "theirs.txt"))
	require.NoError(t, readErr, "what was there has to still be there")
	assert.Equal(t, "mine", string(body))
}

// Nothing was there, so there is nothing to refuse: the ordinary download must
// not start needing --force.
func TestReplacingIntoAFreeNameNeedsNoForce(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "golden-1.0")

	staging, err := os.MkdirTemp(dir, ".azd-dataset-*")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(staging, "rows.jsonl"), []byte("{}\n"), 0o600))

	require.NoError(t, replaceDir(staging, dest, false))

	// #nosec G304 -- both paths are inside this test's own TempDir.
	rows, err := os.ReadFile(filepath.Join(dest, "rows.jsonl"))
	require.NoError(t, err)
	assert.Equal(t, "{}\n", string(rows))
}
