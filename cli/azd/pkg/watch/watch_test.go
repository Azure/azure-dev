// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package watch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

func TestGetFileChanges_AzdxIgnoreFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// Create .azdxignore that excludes *.log files.
	err := os.WriteFile(filepath.Join(dir, ".azdxignore"), []byte("*.log\n"), 0600)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	watcher, err := NewWatcher(ctx)
	require.NoError(t, err)

	// Write an ignored file and a tracked file.
	err = os.WriteFile(filepath.Join(dir, "debug.log"), []byte("log data"), 0600)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0600)
	require.NoError(t, err)

	// The tracked file should appear; the ignored file should not.
	require.Eventually(t, func() bool {
		changes := watcher.GetFileChanges()
		foundMain := false
		for _, c := range changes {
			if filepath.Base(c.Path) == "debug.log" {
				return false // ignored file appeared — fail fast
			}
			if filepath.Base(c.Path) == "main.go" && c.ChangeType == FileCreated {
				foundMain = true
			}
		}
		return foundMain
	}, 2*time.Second, 50*time.Millisecond, "expected main.go created without debug.log")
}

func TestGetFileChanges_AzdxIgnoreDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// Create .azdxignore that excludes the vendor/ directory.
	err := os.WriteFile(filepath.Join(dir, ".azdxignore"), []byte("vendor/\n"), 0600)
	require.NoError(t, err)

	// Pre-create the ignored directory and a file inside it.
	err = os.MkdirAll(filepath.Join(dir, "vendor", "pkg"), 0700)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	watcher, err := NewWatcher(ctx)
	require.NoError(t, err)

	// Write a file inside the ignored directory and a tracked file.
	err = os.WriteFile(filepath.Join(dir, "vendor", "pkg", "lib.go"), []byte("package pkg"), 0600)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(dir, "app.go"), []byte("package main"), 0600)
	require.NoError(t, err)

	// The tracked file should appear; vendor/ files should not.
	require.Eventually(t, func() bool {
		changes := watcher.GetFileChanges()
		foundApp := false
		for _, c := range changes {
			rel, err := filepath.Rel(dir, c.Path)
			if err == nil && (rel == "vendor" || strings.HasPrefix(filepath.ToSlash(rel), "vendor/")) {
				return false // vendor file appeared — fail fast
			}
			if filepath.Base(c.Path) == "app.go" && c.ChangeType == FileCreated {
				foundApp = true
			}
		}
		return foundApp
	}, 2*time.Second, 50*time.Millisecond, "expected app.go created without vendor/ files")
}

func TestGetFileChanges_NoAzdxIgnoreFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// No .azdxignore file — watcher should still start without errors.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	watcher, err := NewWatcher(ctx)
	require.NoError(t, err)

	// All files should be tracked when no ignore file exists.
	err = os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("hello"), 0600)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		for _, c := range watcher.GetFileChanges() {
			if filepath.Base(c.Path) == "tracked.txt" && c.ChangeType == FileCreated {
				return true
			}
		}
		return false
	}, 2*time.Second, 50*time.Millisecond, "expected created file tracked.txt")
}

func TestGetFileChanges_GitIgnoreRespected(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// Create .gitignore that excludes *.tmp files.
	err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.tmp\n"), 0600)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	watcher, err := NewWatcher(ctx)
	require.NoError(t, err)

	// Write an ignored file and a tracked file.
	err = os.WriteFile(filepath.Join(dir, "cache.tmp"), []byte("temp"), 0600)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(dir, "main.txt"), []byte("content"), 0600)
	require.NoError(t, err)

	// The tracked file should appear.
	require.Eventually(t, func() bool {
		for _, c := range watcher.GetFileChanges() {
			if filepath.Base(c.Path) == "main.txt" && c.ChangeType == FileCreated {
				return true
			}
		}
		return false
	}, 2*time.Second, 50*time.Millisecond, "expected created file main.txt")

	// Verify the ignored file is not in the changes.
	for _, c := range watcher.GetFileChanges() {
		require.NotEqual(t, "cache.tmp", filepath.Base(c.Path),
			"cache.tmp should be ignored by .gitignore")
	}
}

func TestIsIgnored_MatcherIntegration(t *testing.T) {
	// Direct test of the ignore matcher as used by the watcher.
	// This tests the Relative() code path (not Absolute()) and verifies
	// that the matcher is wired correctly into the fileWatcher.
	dir := t.TempDir()
	t.Chdir(dir)

	err := os.WriteFile(filepath.Join(dir, ".azdxignore"), []byte("dist/\n*.bak\n"), 0600)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	w, err := NewWatcher(ctx)
	require.NoError(t, err)

	fw, ok := w.(*fileWatcher)
	require.True(t, ok, "expected NewWatcher to return *fileWatcher")

	// Verify the matcher is loaded and works with relative paths.
	require.True(t, fw.ignoreMatcher.IsIgnored("dist", true))
	require.True(t, fw.ignoreMatcher.IsIgnored("file.bak", false))
	require.False(t, fw.ignoreMatcher.IsIgnored("src/main.go", false))
}

func TestGetFileChanges_CreateThenDelete(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	watcher, err := NewWatcher(ctx)
	require.NoError(t, err)

	// Create a file and wait for it to appear in changes.
	testFile := filepath.Join(dir, "ephemeral.txt")
	err = os.WriteFile(testFile, []byte("short-lived"), 0600)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		for _, c := range watcher.GetFileChanges() {
			if filepath.Base(c.Path) == "ephemeral.txt" && c.ChangeType == FileCreated {
				return true
			}
		}
		return false
	}, 2*time.Second, 50*time.Millisecond, "expected created file ephemeral.txt")

	// Delete the file — it should be removed from Created, not added to Deleted.
	err = os.Remove(testFile)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		for _, c := range watcher.GetFileChanges() {
			if filepath.Base(c.Path) == "ephemeral.txt" {
				return false // still present — wait
			}
		}
		return true // gone from all change maps
	}, 2*time.Second, 50*time.Millisecond,
		"ephemeral.txt should be removed from Created after delete, not moved to Deleted")
}

func TestGetFileChanges_RenameFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	watcher, err := NewWatcher(ctx)
	require.NoError(t, err)

	oldFile := filepath.Join(dir, "old.txt")
	err = os.WriteFile(oldFile, []byte("content"), 0600)
	require.NoError(t, err)

	// Wait for the initial create event.
	require.Eventually(t, func() bool {
		for _, c := range watcher.GetFileChanges() {
			if filepath.Base(c.Path) == "old.txt" {
				return true
			}
		}
		return false
	}, 2*time.Second, 50*time.Millisecond, "expected old.txt to appear")

	// Rename the file.
	newFile := filepath.Join(dir, "new.txt")
	err = os.Rename(oldFile, newFile)
	require.NoError(t, err)

	// The new name should appear in changes eventually.
	require.Eventually(t, func() bool {
		for _, c := range watcher.GetFileChanges() {
			if filepath.Base(c.Path) == "new.txt" {
				return true
			}
		}
		return false
	}, 2*time.Second, 50*time.Millisecond, "expected new.txt after rename")
}

func TestGetFileChanges_DeleteIgnoredDirFallback(t *testing.T) {
	// Tests the os.Stat failure fallback: when a directory matching a dir-only
	// ignore pattern (trailing /) is deleted, os.Stat fails and isDir defaults
	// to false. The watcher re-checks with isDir=true so that the Remove event
	// is still filtered by the directory-only pattern.
	dir := t.TempDir()
	t.Chdir(dir)

	// .azdxignore uses a dir-only pattern (trailing slash).
	err := os.WriteFile(filepath.Join(dir, ".azdxignore"), []byte("tmpout/\n"), 0600)
	require.NoError(t, err)

	// Pre-create the directory so watchRecursive skips it (ignored).
	err = os.MkdirAll(filepath.Join(dir, "tmpout"), 0700)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	watcher, err := NewWatcher(ctx)
	require.NoError(t, err)

	// Delete the ignored directory — the parent watcher fires a Remove event.
	// os.Stat will fail (path gone), so isDir defaults to false.
	// Without the fallback re-check (isDir=true), this would leak through
	// as a file deletion since "tmpout/" only matches directories.
	err = os.RemoveAll(filepath.Join(dir, "tmpout"))
	require.NoError(t, err)

	// Write a tracked file as a positive signal.
	err = os.WriteFile(filepath.Join(dir, "tracked.go"), []byte("package main"), 0600)
	require.NoError(t, err)

	// Verify tracked file appears and no tmpout path leaks through.
	require.Eventually(t, func() bool {
		changes := watcher.GetFileChanges()
		foundTracked := false
		for _, c := range changes {
			if filepath.Base(c.Path) == "tmpout" {
				return false // ignored dir leaked through — fail fast
			}
			if filepath.Base(c.Path) == "tracked.go" && c.ChangeType == FileCreated {
				foundTracked = true
			}
		}
		return foundTracked
	}, 2*time.Second, 50*time.Millisecond,
		"expected tracked.go created without tmpout directory delete leaking through")
}
