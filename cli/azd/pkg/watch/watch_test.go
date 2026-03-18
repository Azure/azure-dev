// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package watch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGetFileChanges_Empty(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	watcher, err := NewWatcher(ctx)
	require.NoError(t, err)

	changes := watcher.GetFileChanges()
	require.Empty(t, changes)
}

func TestGetFileChanges_CreatedFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	watcher, err := NewWatcher(ctx)
	require.NoError(t, err)

	testFile := filepath.Join(dir, "test.txt")
	err = os.WriteFile(testFile, []byte("hello"), 0600)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		for _, c := range watcher.GetFileChanges() {
			if filepath.Base(c.Path) == "test.txt" && c.ChangeType == FileCreated {
				return true
			}
		}
		return false
	}, 2*time.Second, 50*time.Millisecond, "expected created file test.txt")
}

func TestGetFileChanges_ModifiedFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	testFile := filepath.Join(dir, "existing.txt")
	err := os.WriteFile(testFile, []byte("original"), 0600)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	watcher, err := NewWatcher(ctx)
	require.NoError(t, err)

	err = os.WriteFile(testFile, []byte("modified"), 0600)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		for _, c := range watcher.GetFileChanges() {
			if filepath.Base(c.Path) == "existing.txt" && c.ChangeType == FileModified {
				return true
			}
		}
		return false
	}, 2*time.Second, 50*time.Millisecond, "expected modified file existing.txt")
}

func TestGetFileChanges_DeletedFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	testFile := filepath.Join(dir, "deleteme.txt")
	err := os.WriteFile(testFile, []byte("delete me"), 0600)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	watcher, err := NewWatcher(ctx)
	require.NoError(t, err)

	err = os.Remove(testFile)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		for _, c := range watcher.GetFileChanges() {
			if filepath.Base(c.Path) == "deleteme.txt" && c.ChangeType == FileDeleted {
				return true
			}
		}
		return false
	}, 2*time.Second, 50*time.Millisecond, "expected deleted file deleteme.txt")
}

func TestFileChangeType_Values(t *testing.T) {
	require.Equal(t, FileChangeType(0), FileCreated)
	require.Equal(t, FileChangeType(1), FileModified)
	require.Equal(t, FileChangeType(2), FileDeleted)
}
