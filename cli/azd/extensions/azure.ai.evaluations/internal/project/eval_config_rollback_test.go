// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScaffoldRollbackPreservesOriginalBytesAndPermissions(t *testing.T) {
	for _, original := range []string{"", "\xef\xbb\xbf# preserve BOM and CRLF\r\nx-extra: [one, two]\r\nevals: []\r\n"} {
		t.Run(original, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "custom.yml")
			// #nosec G306 -- exercise preservation of the author's existing permissions.
			require.NoError(t, os.WriteFile(path, []byte(original), 0o640))
			before, err := os.Stat(path)
			require.NoError(t, err)
			unlock, err := LockEvalConfig(t.Context(), path)
			require.NoError(t, err)
			defer unlock()
			undo, err := ApplyScaffoldWithRollback(path, ScaffoldWrite{Evals: []Eval{{Name: "new"}}})
			require.NoError(t, err)
			require.NoError(t, undo())
			got, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, original, string(got))
			after, err := os.Stat(path)
			require.NoError(t, err)
			assert.Equal(t, before.Mode().Perm(), after.Mode().Perm())
		})
	}
}

func TestScaffoldRollbackRefusesMissingOrChangedExistingConfig(t *testing.T) {
	for _, change := range []string{"removed", "modified", "directory"} {
		t.Run(change, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, EvalConfigBase)
			require.NoError(t, os.WriteFile(path, []byte("evals: []\n"), 0o600))
			unlock, err := LockEvalConfig(t.Context(), dir)
			require.NoError(t, err)
			defer unlock()
			undo, err := ApplyScaffoldWithRollback(dir, ScaffoldWrite{Evals: []Eval{{Name: "new"}}})
			require.NoError(t, err)
			switch change {
			case "removed":
				require.NoError(t, os.Remove(path))
			case "modified":
				require.NoError(t, os.WriteFile(path, []byte("# another writer\n"), 0o600))
			case "directory":
				require.NoError(t, os.Remove(path))
				require.NoError(t, os.Mkdir(path, 0o700))
			}
			require.Error(t, undo())
			switch change {
			case "removed":
				assert.NoFileExists(t, path)
			case "modified":
				got, err := os.ReadFile(path)
				require.NoError(t, err)
				assert.Equal(t, "# another writer\n", string(got))
			case "directory":
				assert.DirExists(t, path)
			}
		})
	}
}

func TestScaffoldRollbackRestoresSymlinkWithoutChangingTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "shared.yml")
	original := []byte("# shared config\nevals: []\n")
	require.NoError(t, os.WriteFile(target, original, 0o600))
	path := filepath.Join(dir, EvalConfigBase)
	if err := os.Symlink("shared.yml", path); err != nil {
		t.Skipf("creating test symlinks is unavailable: %v", err)
	}
	unlock, err := LockEvalConfig(t.Context(), path)
	require.NoError(t, err)
	defer unlock()
	undo, err := ApplyScaffoldWithRollback(path, ScaffoldWrite{Evals: []Eval{{Name: "new"}}})
	require.NoError(t, err)
	require.NoError(t, undo())
	got, err := os.Readlink(path)
	require.NoError(t, err)
	assert.Equal(t, "shared.yml", got)
	body, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, original, body)
}
