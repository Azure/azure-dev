// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package localio

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBoundedStateReadsAndExecutablePublication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	require.NoError(t, Write(path, []byte("state"), false))
	body, err := Read(path)
	require.NoError(t, err)
	assert.Equal(t, "state", string(body))
	require.NoError(t, MakeExecutable(path))
	for _, invalid := range []string{filepath.Dir(path), path + "-absent", filepath.Join(path, "child")} {
		_, err := Read(invalid)
		require.Error(t, err)
		require.Error(t, MakeExecutable(invalid))
	}
	for _, body := range [][]byte{{0xff}, bytes.Repeat([]byte("a"), 8*1024*1024+1)} {
		require.NoError(t, Write(path, body, true))
		_, err := Read(path)
		require.Error(t, err)
	}
	link := path + "-link"
	if err := os.Symlink(path, link); err == nil {
		_, err := Read(link)
		require.Error(t, err)
		require.Error(t, MakeExecutable(link))
	}
}

func TestAtomicWritePoliciesAndPermissions(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "new", "result.json")
	exists, err := Check(path)
	require.NoError(t, err)
	assert.False(t, exists)
	_, err = os.Stat(filepath.Dir(path))
	require.ErrorIs(t, err, fs.ErrNotExist)
	require.NoError(t, Write(path, []byte("first"), false))
	exists, err = Check(path)
	require.NoError(t, err)
	assert.True(t, exists)
	require.ErrorIs(t, Write(path, []byte("second"), false), fs.ErrExist)
	// #nosec G304 -- Reads only this test's private temporary file.
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "first", string(body))
	require.NoError(t, Write(path, []byte("second"), true))
	// #nosec G304 -- Reads only this test's private temporary file.
	body, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "second", string(body))
	info, err := os.Stat(path)
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		assert.Zero(t, info.Mode().Perm()&0o077)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "result.json", entries[0].Name())
	_, err = Check(filepath.Dir(path))
	require.Error(t, err)
}

func TestAtomicPublicationDoesNotClobberRacingWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.json")
	var winners atomic.Int32
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			err := Write(path, []byte("complete"), false)
			if err == nil {
				winners.Add(1)
			} else {
				assert.ErrorIs(t, err, fs.ErrExist)
			}
		})
	}
	workers.Wait()
	assert.EqualValues(t, 1, winners.Load())
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

func TestAtomicWriteRejectsSymlinksAndChangedDirectories(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	require.NoError(t, os.Mkdir(target, 0o700))
	link := filepath.Join(directory, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := Check(filepath.Join(link, "result"))
	require.Error(t, err)
	require.Error(t, Write(filepath.Join(link, "result"), []byte("no"), false))
	file := filepath.Join(directory, "original")
	require.NoError(t, os.WriteFile(file, []byte("original"), 0o600))
	fileLink := filepath.Join(directory, "file-link")
	require.NoError(t, os.Symlink(file, fileLink))
	for _, replace := range []bool{false, true} {
		require.Error(t, Write(fileLink, []byte("no"), replace))
	}
	// #nosec G304 -- Reads only this test's private temporary file.
	body, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Equal(t, "original", string(body))
	info, err := os.Stat(target)
	require.NoError(t, err)
	require.NoError(t, os.Rename(target, target+"-moved"))
	require.NoError(t, os.Mkdir(target, 0o700))
	require.Error(t, verifyDirectory(target, info))
	require.Error(t, Write(filepath.Join(file, "child"), []byte("no"), false))
	_, err = Check(filepath.Join(file, "child"))
	require.Error(t, err)
	assert.False(t, errors.Is(err, fs.ErrNotExist))
}

func TestAtomicWriteFilesystemFailures(t *testing.T) {
	directory := t.TempDir()
	info, err := os.Stat(directory)
	require.NoError(t, err)
	require.Error(t, verifyDirectory(filepath.Join(directory, "missing"), info))
	root, err := os.OpenRoot(directory)
	require.NoError(t, err)
	require.NoError(t, root.Close())
	_, err = checkFile(root, "file")
	require.Error(t, err)
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission checks require a non-root POSIX user")
	}
	private := filepath.Join(directory, "private")
	require.NoError(t, os.Mkdir(private, 0o500))
	t.Cleanup(func() {
		// #nosec G302 -- Restores owner traversal on a private test directory, not an ordinary file.
		require.NoError(t, os.Chmod(private, 0o700))
	})
	require.Error(t, Write(filepath.Join(private, "result"), []byte("data"), false))
	require.NoError(t, os.Chmod(private, 0))
	_, err = Check(filepath.Join(private, "result"))
	require.Error(t, err)
	require.Error(t, Write(filepath.Join(private, "missing", "result"), []byte("data"), false))
}
