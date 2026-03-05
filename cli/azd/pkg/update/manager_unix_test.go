// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows

package update

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReplaceBinary_CreatesNewInode(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("test requires non-root to force rename failure via directory permissions")
	}

	// When replaceBinary falls back to copyFile (e.g. cross-device rename fails),
	// it must remove the old file first, then create a new one. Truncating in place
	// (same inode) corrupts memory-mapped pages of a running binary and causes
	// macOS to SIGKILL the process.
	//
	// Force the copyFile fallback by making the source directory read-only,
	// which prevents os.Rename from unlinking the source entry.
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	src := filepath.Join(srcDir, "new-binary")
	dst := filepath.Join(dstDir, "azd")

	require.NoError(t, os.WriteFile(src, []byte("new content"), 0o755)) //nolint:gosec // test binary needs exec permission
	require.NoError(t, os.WriteFile(dst, []byte("old content"), 0o755)) //nolint:gosec // test binary needs exec permission

	// Make src directory read-only so os.Rename fails (needs write to unlink src)
	require.NoError(t, os.Chmod(srcDir, 0o555))
	t.Cleanup(func() { os.Chmod(srcDir, 0o755) })

	// Record original inode. Hold the old file open so the OS cannot reuse its
	// inode number — on Linux tmpfs, freed inodes are recycled immediately.
	origInfo, err := os.Stat(dst)
	require.NoError(t, err)
	origIno := origInfo.Sys().(*syscall.Stat_t).Ino

	oldFile, err := os.Open(dst)
	require.NoError(t, err)
	defer oldFile.Close()

	m := &Manager{}
	require.NoError(t, m.replaceBinary(context.Background(), src, dst))

	// After replacement, dst should have a NEW inode (remove+create, not truncate)
	newInfo, err := os.Stat(dst)
	require.NoError(t, err)
	newIno := newInfo.Sys().(*syscall.Stat_t).Ino

	require.NotEqual(t, origIno, newIno,
		"replaceBinary should remove then create (new inode), not truncate in place — "+
			"truncating a running binary causes macOS to SIGKILL the process")

	// The old fd should still read the old content (OS kept the inode alive)
	oldContent, err := io.ReadAll(oldFile)
	require.NoError(t, err)
	require.Equal(t, "old content", string(oldContent))

	// Verify new file has correct content
	copied, err := os.ReadFile(dst)
	require.NoError(t, err)
	require.Equal(t, "new content", string(copied))
}
