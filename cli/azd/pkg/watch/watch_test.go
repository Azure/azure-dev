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

	// Create a file
	testFile := filepath.Join(dir, "test.txt")
	err = os.WriteFile(testFile, []byte("hello"), 0600)
	require.NoError(t, err)

	// Wait for the watcher to pick up the change
	time.Sleep(200 * time.Millisecond)

	changes := watcher.GetFileChanges()
	require.NotEmpty(t, changes)

	found := false
	for _, change := range changes {
		if filepath.Base(change.Path) == "test.txt" && change.ChangeType == FileCreated {
			found = true
			break
		}
	}
	require.True(t, found, "expected to find created file test.txt in changes")
}

func TestGetFileChanges_ModifiedFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// Create a file before starting the watcher
	testFile := filepath.Join(dir, "existing.txt")
	err := os.WriteFile(testFile, []byte("original"), 0600)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	watcher, err := NewWatcher(ctx)
	require.NoError(t, err)

	// Modify the file
	err = os.WriteFile(testFile, []byte("modified"), 0600)
	require.NoError(t, err)

	// Wait for the watcher to pick up the change
	time.Sleep(200 * time.Millisecond)

	changes := watcher.GetFileChanges()
	require.NotEmpty(t, changes)

	found := false
	for _, change := range changes {
		if filepath.Base(change.Path) == "existing.txt" && change.ChangeType == FileModified {
			found = true
			break
		}
	}
	require.True(t, found, "expected to find modified file existing.txt in changes")
}

func TestGetFileChanges_DeletedFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// Create a file before starting the watcher
	testFile := filepath.Join(dir, "deleteme.txt")
	err := os.WriteFile(testFile, []byte("delete me"), 0600)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	watcher, err := NewWatcher(ctx)
	require.NoError(t, err)

	// Delete the file
	err = os.Remove(testFile)
	require.NoError(t, err)

	// Wait for the watcher to pick up the change
	time.Sleep(200 * time.Millisecond)

	changes := watcher.GetFileChanges()
	require.NotEmpty(t, changes)

	found := false
	for _, change := range changes {
		if filepath.Base(change.Path) == "deleteme.txt" && change.ChangeType == FileDeleted {
			found = true
			break
		}
	}
	require.True(t, found, "expected to find deleted file deleteme.txt in changes")
}

func TestFileChangeType_Values(t *testing.T) {
	require.Equal(t, FileChangeType(0), FileCreated)
	require.Equal(t, FileChangeType(1), FileModified)
	require.Equal(t, FileChangeType(2), FileDeleted)
}
