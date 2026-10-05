// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package watch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/ignore"
	"github.com/fsnotify/fsnotify"
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

func TestGetFileChanges_ReconcilesMissingCreatedFile(t *testing.T) {
	// Model a missed Remove event without starting a backend or waiting for
	// a ticker: the first snapshot must exclude the stale Created entry.
	dir := t.TempDir()
	missing := filepath.Join(dir, "gone.txt")
	require.NoError(t, os.WriteFile(missing, []byte("x"), 0600))
	require.NoError(t, os.Remove(missing))

	fw := &fileWatcher{fileChanges: &fileChanges{
		Created:  map[string]bool{missing: true},
		Modified: map[string]bool{},
		Deleted:  map[string]bool{},
	}}

	changes := fw.GetFileChanges()
	require.Empty(t, changes, "a missing file must be cleared entirely, not moved to Deleted")
	require.Empty(t, fw.fileChanges.Created, "missing paths must be reclaimed")
	require.Empty(t, fw.GetFileChanges(), "later snapshots must not restore the removed entry")
}

func TestGetFileChanges_PreservesExistingCreatedFile(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "present.txt")
	require.NoError(t, os.WriteFile(present, []byte("x"), 0600))

	fw := &fileWatcher{fileChanges: &fileChanges{
		Created:  map[string]bool{present: true},
		Modified: map[string]bool{},
		Deleted:  map[string]bool{},
	}}

	changes := fw.GetFileChanges()
	require.Len(t, changes, 1)
	require.Equal(t, present, changes[0].Path)
	require.Equal(t, FileCreated, changes[0].ChangeType)
}

func TestGetFileChanges_LateRemoveDoesNotResurrectDeletedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ephemeral.txt")
	fw := &fileWatcher{fileChanges: &fileChanges{
		Created:  map[string]bool{},
		Modified: map[string]bool{},
		Deleted:  map[string]bool{},
	}}
	applyEvent := func(op fsnotify.Op) {
		fw.mu.Lock()
		defer fw.mu.Unlock()
		fw.trackFileEventLocked(fsnotify.Event{Name: path, Op: op})
	}

	require.NoError(t, os.WriteFile(path, []byte("x"), 0600))
	applyEvent(fsnotify.Create)
	require.NoError(t, os.Remove(path))
	require.Empty(t, fw.GetFileChanges())

	applyEvent(fsnotify.Write)
	require.Empty(t, fw.GetFileChanges(), "a queued Write must not reclassify the ephemeral file as Modified")
	applyEvent(fsnotify.Remove)
	require.Empty(t, fw.GetFileChanges(), "a late Remove must not resurrect an ephemeral file as Deleted")
	require.Empty(t, fw.fileChanges.Created)
	require.Empty(t, fw.fileChanges.Deleted)
}

func TestGetFileChanges_LiveNewFileRemainsTrackedAcrossSnapshots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "live.txt")
	fw := &fileWatcher{fileChanges: &fileChanges{
		Created:  map[string]bool{},
		Modified: map[string]bool{},
		Deleted:  map[string]bool{},
	}}
	require.NoError(t, os.WriteFile(path, []byte("x"), 0600))
	for _, op := range []fsnotify.Op{fsnotify.Create, fsnotify.Write, fsnotify.Rename} {
		fw.mu.Lock()
		fw.trackFileEventLocked(fsnotify.Event{Name: path, Op: op})
		fw.mu.Unlock()
		for range 2 {
			require.Equal(t, FileChanges{{Path: path, ChangeType: FileCreated}}, fw.GetFileChanges())
		}
	}
	require.NoError(t, os.Remove(path))
	require.Empty(t, fw.GetFileChanges())
	fw.mu.Lock()
	fw.trackFileEventLocked(fsnotify.Event{Name: path, Op: fsnotify.Remove})
	fw.mu.Unlock()
	require.Empty(t, fw.GetFileChanges())
}

func TestGetFileChanges_NewPopulatedDirectory(t *testing.T) {
	root := t.TempDir()
	initial := filepath.Join(root, "initial.txt")
	require.NoError(t, os.WriteFile(initial, []byte("x"), 0600))
	ignoreFile := filepath.Join(root, ".azdxignore")
	require.NoError(t, os.WriteFile(ignoreFile, []byte("*.log\n"), 0600))
	matcher, err := ignore.NewMatcher(root)
	require.NoError(t, err)
	backend, err := fsnotify.NewWatcher()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, backend.Close()) })
	fw := &fileWatcher{
		root:          root,
		initialFiles:  map[string]struct{}{},
		ignoreMatcher: matcher,
		fileChanges: &fileChanges{
			Created:  map[string]bool{},
			Modified: map[string]bool{},
			Deleted:  map[string]bool{},
		},
	}
	require.NoError(t, fw.watchRecursive(root, backend, true))
	require.Equal(t, map[string]struct{}{initial: {}, ignoreFile: {}}, fw.initialFiles,
		"the initial inventory must contain files, not directories")
	dir := filepath.Join(root, "new-directory")
	require.NoError(t, os.Mkdir(dir, 0700))
	child := filepath.Join(dir, "child.txt")
	require.NoError(t, os.WriteFile(child, []byte("x"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ignored.log"), []byte("x"), 0600))

	fw.mu.Lock()
	err = fw.watchRecursive(dir, backend, false)
	fw.mu.Unlock()
	require.NoError(t, err)
	require.Equal(t, map[string]struct{}{initial: {}, ignoreFile: {}}, fw.initialFiles,
		"new children must not extend the fixed initial inventory")
	require.Equal(t, FileChanges{{Path: child, ChangeType: FileCreated}}, fw.GetFileChanges())
	for _, op := range []fsnotify.Op{fsnotify.Write, fsnotify.Rename} {
		fw.mu.Lock()
		fw.trackFileEventLocked(fsnotify.Event{Name: child, Op: op})
		fw.mu.Unlock()
		require.Equal(t, FileChanges{{Path: child, ChangeType: FileCreated}}, fw.GetFileChanges())
	}

	require.NoError(t, os.Remove(child))
	require.Empty(t, fw.GetFileChanges())
	fw.mu.Lock()
	fw.trackFileEventLocked(fsnotify.Event{Name: child, Op: fsnotify.Remove})
	fw.mu.Unlock()
	require.Empty(t, fw.GetFileChanges())
}

func TestGetFileChanges_DirectoryReplacementDoesNotAppearAsCreated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replaced")
	require.NoError(t, os.Mkdir(path, 0700))
	fw := &fileWatcher{fileChanges: &fileChanges{
		Created:  map[string]bool{path: true},
		Modified: map[string]bool{},
		Deleted:  map[string]bool{},
	}}
	require.Empty(t, fw.GetFileChanges())
	require.Empty(t, fw.fileChanges.Created)
	fw.mu.Lock()
	fw.trackFileEventLocked(fsnotify.Event{Name: path, Op: fsnotify.Remove})
	fw.mu.Unlock()
	require.Empty(t, fw.GetFileChanges())
}

func TestGetFileChanges_FinalSnapshotAfterCancellation(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	ctx, cancel := context.WithCancel(t.Context())
	w, err := NewWatcher(ctx)
	require.NoError(t, err)
	cancel()
	fw, ok := w.(*fileWatcher)
	require.True(t, ok)
	path := filepath.Join(dir, "gone.txt")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0600))
	fw.mu.Lock()
	fw.trackFileEventLocked(fsnotify.Event{Name: path, Op: fsnotify.Create})
	fw.mu.Unlock()
	require.NoError(t, os.Remove(path))
	require.Empty(t, fw.GetFileChanges(), "a final snapshot must reclaim paths even after the event loop is canceled")
	fw.mu.Lock()
	retained := len(fw.fileChanges.Created)
	fw.mu.Unlock()
	require.Zero(t, retained)
}

func TestGetFileChanges_ReclaimsUniqueEphemeralPaths(t *testing.T) {
	dir := t.TempDir()
	initial := filepath.Join(dir, "initial.txt")
	require.NoError(t, os.WriteFile(initial, []byte("x"), 0600))
	fw := &fileWatcher{
		initialFiles: map[string]struct{}{initial: {}},
		fileChanges: &fileChanges{
			Created:  map[string]bool{},
			Modified: map[string]bool{},
			Deleted:  map[string]bool{},
		},
	}
	paths := make([]string, 512)
	for i := range paths {
		path := filepath.Join(dir, fmt.Sprintf("ephemeral-%d.txt", i))
		paths[i] = path
		require.NoError(t, os.WriteFile(path, []byte("x"), 0600))
		fw.mu.Lock()
		fw.trackFileEventLocked(fsnotify.Event{Name: path, Op: fsnotify.Create})
		fw.mu.Unlock()
		require.NoError(t, os.Remove(path))
	}

	require.Empty(t, fw.GetFileChanges())
	require.Zero(t, len(fw.fileChanges.Created), "quiescent missing paths must not accumulate indefinitely")
	for _, path := range paths {
		fw.mu.Lock()
		fw.trackFileEventLocked(fsnotify.Event{Name: path, Op: fsnotify.Write})
		fw.trackFileEventLocked(fsnotify.Event{Name: path, Op: fsnotify.Rename})
		fw.trackFileEventLocked(fsnotify.Event{Name: path, Op: fsnotify.Remove})
		fw.mu.Unlock()
	}
	require.Empty(t, fw.GetFileChanges())
	require.Empty(t, fw.fileChanges.Created)
	require.Empty(t, fw.fileChanges.Modified)
	require.Empty(t, fw.fileChanges.Deleted)
	require.Equal(t, map[string]struct{}{initial: {}}, fw.initialFiles,
		"session accounting must not retain historical ephemeral paths in another map")
}

func TestGetFileChanges_RecreatedFilePreservesCreatedAccounting(t *testing.T) {
	for _, lateRemove := range []bool{false, true} {
		name := "missed Remove"
		if lateRemove {
			name = "queued Remove before recreated Create"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "recreated.txt")
			fw := &fileWatcher{fileChanges: &fileChanges{
				Created:  map[string]bool{},
				Modified: map[string]bool{},
				Deleted:  map[string]bool{},
			}}
			applyEvent := func(op fsnotify.Op) {
				fw.mu.Lock()
				defer fw.mu.Unlock()
				fw.trackFileEventLocked(fsnotify.Event{Name: path, Op: op})
			}

			require.NoError(t, os.WriteFile(path, []byte("first"), 0600))
			applyEvent(fsnotify.Create)
			require.NoError(t, os.Remove(path))
			require.Empty(t, fw.GetFileChanges())
			require.Empty(t, fw.GetFileChanges())

			require.NoError(t, os.WriteFile(path, []byte("second"), 0600))
			if lateRemove {
				applyEvent(fsnotify.Remove)
			}
			applyEvent(fsnotify.Create)
			applyEvent(fsnotify.Write)
			require.Equal(t, FileChanges{{Path: path, ChangeType: FileCreated}}, fw.GetFileChanges())
			require.Empty(t, fw.fileChanges.Modified)
			require.Empty(t, fw.fileChanges.Deleted)

			require.NoError(t, os.Remove(path))
			require.Empty(t, fw.GetFileChanges())
			applyEvent(fsnotify.Remove)
			require.Empty(t, fw.GetFileChanges())
		})
	}
}

func TestGetFileChanges_PreExistingRemovalRemainsDeleted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.txt")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0600))
	fw := &fileWatcher{
		initialFiles: map[string]struct{}{path: {}},
		fileChanges: &fileChanges{
			Created:  map[string]bool{},
			Modified: map[string]bool{},
			Deleted:  map[string]bool{},
		},
	}
	applyEvent := func(op fsnotify.Op) {
		fw.mu.Lock()
		defer fw.mu.Unlock()
		fw.trackFileEventLocked(fsnotify.Event{Name: path, Op: op})
	}

	applyEvent(fsnotify.Write)
	require.Equal(t, FileChanges{{Path: path, ChangeType: FileModified}}, fw.GetFileChanges())
	applyEvent(fsnotify.Rename)
	require.Equal(t, FileChanges{{Path: path, ChangeType: FileModified}}, fw.GetFileChanges())
	require.NoError(t, os.Remove(path))
	require.Equal(t, FileChanges{{Path: path, ChangeType: FileModified}}, fw.GetFileChanges())
	applyEvent(fsnotify.Remove)
	require.Equal(t, FileChanges{{Path: path, ChangeType: FileDeleted}}, fw.GetFileChanges())
	require.Empty(t, fw.fileChanges.Modified)
}

func TestGetFileChanges_ReconciliationPreservesOtherChanges(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "a-created.txt")
	modified := filepath.Join(dir, "b-modified.txt")
	deleted := filepath.Join(dir, "c-deleted.txt")

	for _, exists := range []bool{false, true} {
		name := "missing modified file"
		if exists {
			name = "existing modified file"
		}
		t.Run(name, func(t *testing.T) {
			if exists {
				require.NoError(t, os.WriteFile(modified, []byte("x"), 0600))
			}
			fw := &fileWatcher{fileChanges: &fileChanges{
				Created:  map[string]bool{missing: true},
				Modified: map[string]bool{modified: true},
				Deleted:  map[string]bool{deleted: true},
			}}

			require.Equal(t, FileChanges{
				{Path: modified, ChangeType: FileModified},
				{Path: deleted, ChangeType: FileDeleted},
			}, fw.GetFileChanges())
			require.Empty(t, fw.fileChanges.Created)
			require.Equal(t, map[string]bool{modified: true}, fw.fileChanges.Modified)
			require.Equal(t, map[string]bool{deleted: true}, fw.fileChanges.Deleted)
		})
	}
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
