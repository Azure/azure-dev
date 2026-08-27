// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package osutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_ReadContainedFileWindowsJunctions(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	target := filepath.Join(root, "target")
	outside := filepath.Join(parent, "outside")
	require.NoError(t, os.MkdirAll(target, 0o750))
	require.NoError(t, os.MkdirAll(outside, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "root.txt"), []byte("root"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(target, "inside.txt"), []byte("inside"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "outside.txt"), []byte("outside"), 0o600))

	rootJunction := filepath.Join(parent, "root-link")
	createJunction(t, root, rootJunction)
	data, err := ReadContainedFile([]string{rootJunction}, "root.txt")
	require.NoError(t, err)
	require.Equal(t, []byte("root"), data)

	createJunction(t, target, filepath.Join(root, "inside-link"))
	data, err = ReadContainedFile([]string{root}, "inside-link/inside.txt")
	require.NoError(t, err)
	require.Equal(t, []byte("inside"), data)

	createJunction(t, outside, filepath.Join(root, "outside-link"))
	_, err = ReadContainedFile([]string{root}, "outside-link/outside.txt")
	require.ErrorContains(t, err, "resolves outside all root directories")
}

func Test_ReadContainedFileWindowsDoesNotFallbackAfterJunctionEscape(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	firstRoot := filepath.Join(parent, "first")
	secondRoot := filepath.Join(parent, "second")
	outside := filepath.Join(parent, "outside")
	require.NoError(t, os.MkdirAll(firstRoot, 0o750))
	require.NoError(t, os.MkdirAll(secondRoot, 0o750))
	require.NoError(t, os.MkdirAll(outside, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "file.txt"), []byte("outside"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(secondRoot, "file.txt"), []byte("fallback"), 0o600))
	createJunction(t, outside, filepath.Join(firstRoot, "linked"))

	_, err := ReadContainedFile([]string{firstRoot, secondRoot}, "linked/file.txt")
	require.ErrorContains(t, err, "resolves outside all root directories")
}

func Test_ReadContainedFileWindowsDoesNotFallbackAfterJunctionEscapeWithMissingLeaf(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	firstRoot := filepath.Join(parent, "first")
	secondRoot := filepath.Join(parent, "second")
	outside := filepath.Join(parent, "outside")
	require.NoError(t, os.MkdirAll(firstRoot, 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(secondRoot, "linked"), 0o750))
	require.NoError(t, os.MkdirAll(outside, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(secondRoot, "linked", "missing.txt"), []byte("fallback"), 0o600))
	createJunction(t, outside, filepath.Join(firstRoot, "linked"))
	require.NoError(t, os.Remove(outside))

	_, err := ReadContainedFile([]string{firstRoot, secondRoot}, "linked/missing.txt")
	require.ErrorContains(t, err, "resolves outside all root directories")
}

func Test_ReadContainedFileWindowsRootRenameUsesPinnedHandle(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	rootPath := filepath.Join(parent, "root")
	movedPath := filepath.Join(parent, "moved")
	require.NoError(t, os.MkdirAll(rootPath, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(rootPath, "file.txt"), []byte("original"), 0o600))

	root, err := openRootFile(rootPath)
	require.NoError(t, err)
	defer root.Close()
	require.NoError(t, os.Rename(rootPath, movedPath))
	require.NoError(t, os.MkdirAll(rootPath, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(rootPath, "file.txt"), []byte("replacement"), 0o600))

	data, err := readContainedFileFromRoot(root, "file.txt")
	require.NoError(t, err)
	require.Equal(t, []byte("original"), data)
}

func Test_IsCanonicalPathContainedWindowsRoots(t *testing.T) {
	t.Parallel()

	tests := []struct {
		root   string
		target string
		want   bool
	}{
		{root: `C:\`, target: `C:\file.txt`, want: true},
		{root: `C:\`, target: `D:\file.txt`, want: false},
		{root: `\\server\share\`, target: `\\server\share\folder\file.txt`, want: true},
		{root: `\\server\share\`, target: `\\server\share-other\file.txt`, want: false},
		{root: `C:\Root`, target: `C:\root\file.txt`, want: false},
	}

	for _, test := range tests {
		require.Equal(t, test.want, isCanonicalPathContained(test.root, test.target))
	}
}

func createJunction(t *testing.T, target, link string) {
	t.Helper()
	//nolint:gosec // Test paths are created under t.TempDir and are not shell-expanded.
	output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", link, target).CombinedOutput()
	require.NoErrorf(t, err, "create junction: %s", output)
}
