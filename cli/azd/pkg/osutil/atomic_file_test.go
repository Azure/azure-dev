// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package osutil

import (
	"context"
	"fmt"
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
	symlinkOrSkip(t, filepath.Base(targetPath), linkPath)

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
	symlinkOrSkip(t, filepath.Base(targetPath), intermediatePath)
	symlinkOrSkip(t, filepath.Base(intermediatePath), linkPath)

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

func Test_WriteFileAtomic_ResolvesRelativeTargetAfterParentSymlink(t *testing.T) {
	for _, targetExists := range []bool{false, true} {
		t.Run(fmt.Sprintf("target-exists-%t", targetExists), func(t *testing.T) {
			dir := t.TempDir()
			realDir := filepath.Join(dir, "real")
			realSubdir := filepath.Join(realDir, "sub")
			require.NoError(t, os.MkdirAll(realSubdir, PermissionDirectory))

			aliasPath := filepath.Join(dir, "alias")
			symlinkOrSkip(t, filepath.Join("real", "sub"), aliasPath)

			targetPath := filepath.Join(realDir, "target.json")
			if targetExists {
				require.NoError(t, os.WriteFile(targetPath, []byte("old"), PermissionFile))
			}

			decoyPath := filepath.Join(dir, "target.json")
			require.NoError(t, os.WriteFile(decoyPath, []byte("decoy"), PermissionFile))

			linkPath := filepath.Join(aliasPath, "config.json")
			symlinkOrSkip(t, filepath.Join("..", "target.json"), linkPath)

			require.NoError(t, WriteFileAtomic(t.Context(), linkPath, []byte("new"), 0))

			contents, err := os.ReadFile(targetPath)
			require.NoError(t, err)
			require.Equal(t, "new", string(contents))

			decoyContents, err := os.ReadFile(decoyPath)
			require.NoError(t, err)
			require.Equal(t, "decoy", string(decoyContents))

			linkInfo, err := os.Lstat(linkPath)
			require.NoError(t, err)
			require.NotZero(t, linkInfo.Mode()&os.ModeSymlink)
		})
	}
}

func Test_WriteFileAtomic_SymlinkCycle(t *testing.T) {
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "first.json")
	secondPath := filepath.Join(dir, "second.json")
	symlinkOrSkip(t, filepath.Base(secondPath), firstPath)
	symlinkOrSkip(t, filepath.Base(firstPath), secondPath)

	err := WriteFileAtomic(t.Context(), firstPath, []byte("new"), 0)
	require.ErrorContains(t, err, "too many symlinks")

	tempFiles, globErr := filepath.Glob(filepath.Join(dir, ".*.tmp-*"))
	require.NoError(t, globErr)
	require.Empty(t, tempFiles)
}

func symlinkOrSkip(t *testing.T, oldName string, newName string) {
	t.Helper()
	if err := os.Symlink(oldName, newName); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("creating symlink requires additional Windows privileges: %v", err)
		}
		require.NoError(t, err)
	}
}
