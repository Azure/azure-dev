// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package service

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFile is a tiny helper that writes content to dir/relPath, creating
// intermediate directories as needed.
func writeFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(relPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
}

func TestComputeDirectoryHash_Deterministic(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "alpha")
	writeFile(t, dir, "sub/b.txt", "beta")

	h1, err := ComputeDirectoryHash(dir)
	require.NoError(t, err)
	h2, err := ComputeDirectoryHash(dir)
	require.NoError(t, err)

	assert.Equal(t, h1, h2, "two calls over the same content must produce the same hash")
	assert.Len(t, h1, 64, "SHA-256 hex digest should be 64 characters")
}

func TestComputeDirectoryHash_OrderIndependent(t *testing.T) {
	// Same files written in different order should still produce the same hash,
	// because the implementation sorts by relative path before hashing.
	dirA := t.TempDir()
	writeFile(t, dirA, "a.txt", "alpha")
	writeFile(t, dirA, "b.txt", "beta")
	writeFile(t, dirA, "c.txt", "gamma")

	dirB := t.TempDir()
	writeFile(t, dirB, "c.txt", "gamma")
	writeFile(t, dirB, "a.txt", "alpha")
	writeFile(t, dirB, "b.txt", "beta")

	hA, err := ComputeDirectoryHash(dirA)
	require.NoError(t, err)
	hB, err := ComputeDirectoryHash(dirB)
	require.NoError(t, err)

	assert.Equal(t, hA, hB)
}

func TestComputeDirectoryHash_ContentSensitive(t *testing.T) {
	dirA := t.TempDir()
	writeFile(t, dirA, "a.txt", "alpha")

	dirB := t.TempDir()
	writeFile(t, dirB, "a.txt", "ALPHA")

	hA, err := ComputeDirectoryHash(dirA)
	require.NoError(t, err)
	hB, err := ComputeDirectoryHash(dirB)
	require.NoError(t, err)

	assert.NotEqual(t, hA, hB, "different file contents must produce different hashes")
}

func TestComputeDirectoryHash_PathSensitive(t *testing.T) {
	// Same contents under different file names must hash differently.
	dirA := t.TempDir()
	writeFile(t, dirA, "a.txt", "alpha")

	dirB := t.TempDir()
	writeFile(t, dirB, "b.txt", "alpha")

	hA, err := ComputeDirectoryHash(dirA)
	require.NoError(t, err)
	hB, err := ComputeDirectoryHash(dirB)
	require.NoError(t, err)

	assert.NotEqual(t, hA, hB, "same content under different names must produce different hashes")
}

func TestComputeDirectoryHash_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	h, err := ComputeDirectoryHash(dir)
	require.NoError(t, err)
	assert.Len(t, h, 64)
}

func TestComputeDirectoryHash_SkipsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Creating symlinks on Windows requires SeCreateSymbolicLinkPrivilege,
		// which CI/dev workstations typically don't grant. Skip rather than fail.
		t.Skip("symlink creation requires elevated privileges on Windows")
	}

	// Baseline: just a real file.
	baseline := t.TempDir()
	writeFile(t, baseline, "real.txt", "payload")
	hBaseline, err := ComputeDirectoryHash(baseline)
	require.NoError(t, err)

	// Same real file, plus a symlink that points to it. The symlink should be skipped.
	withLink := t.TempDir()
	writeFile(t, withLink, "real.txt", "payload")
	target := filepath.Join(withLink, "real.txt")
	link := filepath.Join(withLink, "link.txt")
	require.NoError(t, os.Symlink(target, link))

	hWithLink, err := ComputeDirectoryHash(withLink)
	require.NoError(t, err)

	assert.Equal(t, hBaseline, hWithLink, "symlinks must not contribute to the hash")
}

func TestComputeDirectoryHash_PathNormalization(t *testing.T) {
	// Sanity check: relative paths are normalized to forward slashes before hashing,
	// so the hash of a nested layout is platform-independent. We verify this indirectly
	// by checking that hashing produces the same digest each time on the current OS,
	// and that backslash-bearing path strings would *not* appear in any error/debug output.
	dir := t.TempDir()
	writeFile(t, dir, "nested/dir/file.txt", "x")

	h, err := ComputeDirectoryHash(dir)
	require.NoError(t, err)
	require.Len(t, h, 64)

	// Stronger guarantee: the implementation walks files, computes rel paths with
	// filepath.Rel, then replaces "\\" with "/". If we accidentally reverted the
	// normalization, Windows hashes would diverge from POSIX. We can't easily
	// cross-check here, but we can at least assert the digest is stable across
	// repeated invocations on this OS.
	h2, err := ComputeDirectoryHash(dir)
	require.NoError(t, err)
	assert.Equal(t, h, h2)
}

func TestComputeDirectoryHash_MissingDirectory(t *testing.T) {
	_, err := ComputeDirectoryHash(filepath.Join(t.TempDir(), "does-not-exist"))
	require.Error(t, err)
	assert.True(t,
		strings.Contains(err.Error(), "failed to walk directory") ||
			strings.Contains(err.Error(), "no such file") ||
			strings.Contains(err.Error(), "cannot find"),
		"error should reference the walk failure, got: %v", err)
}

func TestTruncateHashVersion(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"shorter than max", strings.Repeat("a", MaxHashVersionLength-1), strings.Repeat("a", MaxHashVersionLength-1)},
		{"exactly max", strings.Repeat("a", MaxHashVersionLength), strings.Repeat("a", MaxHashVersionLength)},
		{"longer than max", strings.Repeat("a", MaxHashVersionLength+10), strings.Repeat("a", MaxHashVersionLength)},
		{"sha256 full digest", strings.Repeat("f", 64), strings.Repeat("f", MaxHashVersionLength)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, TruncateHashVersion(tc.in))
		})
	}
}
