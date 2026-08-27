// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package osutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_ReadContainedFileNonWindowsRootRenameUsesPinnedHandle(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	rootPath := filepath.Join(parent, "root")
	movedPath := filepath.Join(parent, "moved")
	require.NoError(t, os.MkdirAll(rootPath, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(rootPath, "file.txt"), []byte("original"), 0o600))

	root, err := os.OpenRoot(rootPath)
	require.NoError(t, err)
	defer root.Close()
	require.NoError(t, os.Rename(rootPath, movedPath))
	require.NoError(t, os.MkdirAll(rootPath, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(rootPath, "file.txt"), []byte("replacement"), 0o600))

	data, err := readContainedFileFromRoot(root, "file.txt")
	require.NoError(t, err)
	require.Equal(t, []byte("original"), data)
}

func Test_ReadContainedFileNonWindowsDoesNotFallbackAfterSymlinkEscape(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	firstRoot := filepath.Join(parent, "first")
	secondRoot := filepath.Join(parent, "second")
	outside := filepath.Join(parent, "outside")
	require.NoError(t, os.MkdirAll(firstRoot, 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(secondRoot, "linked"), 0o750))
	require.NoError(t, os.MkdirAll(outside, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "file.txt"), []byte("outside"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(secondRoot, "linked", "file.txt"), []byte("fallback"), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(firstRoot, "linked")))

	_, err := ReadContainedFile([]string{firstRoot, secondRoot}, "linked/file.txt")
	require.Error(t, err)
}
