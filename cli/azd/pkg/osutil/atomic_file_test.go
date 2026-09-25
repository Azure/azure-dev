// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package osutil

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_WriteFileAtomic_ReplacesContentsAndPreservesPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o600))

	require.NoError(t, WriteFileAtomic(t.Context(), path, []byte("new"), 0))

	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "new", string(contents))

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
}

func Test_WriteFileAtomic_CanceledContextPreservesContents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o600))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, WriteFileAtomic(ctx, path, []byte("new"), 0), context.Canceled)

	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "old", string(contents))
}

func Test_WriteFileAtomic_PreservesSymlink(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "target.json")
	linkPath := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(targetPath, []byte("old"), 0o600))
	if err := os.Symlink(filepath.Base(targetPath), linkPath); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("creating symlink requires additional Windows privileges: %v", err)
		}
		require.NoError(t, err)
	}

	require.NoError(t, WriteFileAtomic(t.Context(), linkPath, []byte("new"), 0))

	linkInfo, err := os.Lstat(linkPath)
	require.NoError(t, err)
	require.NotZero(t, linkInfo.Mode()&os.ModeSymlink)

	contents, err := os.ReadFile(targetPath)
	require.NoError(t, err)
	require.Equal(t, "new", string(contents))
}

func Test_WriteFileAtomic_PreservesDanglingSymlinkChain(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "target.json")
	intermediatePath := filepath.Join(dir, "intermediate.json")
	linkPath := filepath.Join(dir, "config.json")
	if err := os.Symlink(filepath.Base(targetPath), intermediatePath); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("creating symlink requires additional Windows privileges: %v", err)
		}
		require.NoError(t, err)
	}
	require.NoError(t, os.Symlink(filepath.Base(intermediatePath), linkPath))

	require.NoError(t, WriteFileAtomic(t.Context(), linkPath, []byte("new"), 0))

	for _, path := range []string{linkPath, intermediatePath} {
		linkInfo, err := os.Lstat(path)
		require.NoError(t, err)
		require.NotZero(t, linkInfo.Mode()&os.ModeSymlink)
	}

	contents, err := os.ReadFile(targetPath)
	require.NoError(t, err)
	require.Equal(t, "new", string(contents))
}
