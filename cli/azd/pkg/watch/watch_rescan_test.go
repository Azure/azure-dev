// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package watch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/require"
)

func TestFinalSnapshot_JoinsQueuedAndInFlightDiscovery(t *testing.T) {
	fw, backend := startupFixture(t)
	marker := filepath.Join(fw.root, "initial.txt")
	require.NoError(t, os.WriteFile(marker, []byte("x"), 0600))
	entered := make(chan string, 2)
	release := make(chan struct{}, 2)
	seen := make(map[string]bool)
	backend.add = func(path string) error {
		if path != fw.root && !seen[path] {
			seen[path] = true
			entered <- path
			<-release
			for range 51 {
				backend.events <- fsnotify.Event{Name: marker, Op: fsnotify.Write}
			}
		}
		return nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	require.NoError(t, fw.start(ctx, backend, backend.events, backend.errors))
	t.Cleanup(func() {
		cancel()
		close(release)
		waitStartupExit(t, fw.done)
	})
	children := make([]string, 2)
	for i := range 2 {
		dir := filepath.Join(fw.root, fmt.Sprintf("new-%d", i))
		require.NoError(t, os.Mkdir(dir, 0700))
		children[i] = filepath.Join(dir, "child.txt")
		require.NoError(t, os.WriteFile(children[i], []byte("x"), 0600))
		backend.events <- fsnotify.Event{Name: dir, Op: fsnotify.Create}
		if i == 0 {
			select {
			case path := <-entered:
				require.Equal(t, dir, path)
			case <-time.After(2 * time.Second):
				t.Fatal("first subtree did not enter registration")
			}
		}
	}
	backend.events <- fsnotify.Event{Name: marker, Op: fsnotify.Write}
	require.Eventually(t, func() bool {
		fw.mu.Lock()
		defer fw.mu.Unlock()
		return fw.fileChanges.Modified[marker]
	}, 2*time.Second, time.Millisecond)
	snapshot := make(chan FileChanges, 1)
	finished := make(chan struct{})
	go func() {
		snapshot <- fw.GetFileChanges()
		cancel()
		close(finished)
	}()
	select {
	case <-finished:
		t.Fatal("snapshot returned before in-flight registration completed")
	case <-time.After(50 * time.Millisecond):
	}
	release <- struct{}{}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("queued subtree was abandoned")
	}
	select {
	case <-finished:
		t.Fatal("snapshot returned before queued registration completed")
	case <-time.After(50 * time.Millisecond):
	}
	release <- struct{}{}
	waitStartupExit(t, finished)
	waitStartupExit(t, fw.done)
	expected := FileChanges{
		{Path: marker, ChangeType: FileModified},
		{Path: children[0], ChangeType: FileCreated},
		{Path: children[1], ChangeType: FileCreated},
	}
	require.Equal(t, expected, <-snapshot)
	stoppedSnapshot := make(chan FileChanges, 1)
	go func() { stoppedSnapshot <- fw.GetFileChanges() }()
	select {
	case changes := <-stoppedSnapshot:
		require.Equal(t, expected, changes)
	case <-time.After(2 * time.Second):
		t.Fatal("snapshot waited for an owner that already stopped")
	}
	require.NotContains(t, fw.initialFiles, children[0])
}

func TestNewWatcher_RescanContinuesPastDisappearingSibling(t *testing.T) {
	for _, duringAdd := range []bool{false, true} {
		name := "vanished after directory names were captured"
		if duringAdd {
			name = "vanished before Add"
		}
		t.Run(name, func(t *testing.T) {
			fw, backend := startupFixture(t)
			newDir := filepath.Join(fw.root, "z-new")
			transient := filepath.Join(newDir, "a-transient")
			child := filepath.Join(newDir, "a-child.txt")
			marker := filepath.Join(newDir, "z-complete")
			var rescan atomic.Bool
			var registered atomic.Bool
			completed := make(chan struct{}, 1)
			backend.add = func(path string) error {
				if rescan.Load() {
					if !duringAdd && path == newDir {
						if err := os.Remove(transient); err != nil {
							return err
						}
					} else if duringAdd && path == transient {
						if err := os.Remove(transient); err != nil {
							return err
						}
						return &os.PathError{Op: "Add", Path: path, Err: os.ErrNotExist}
					}
				}
				if path == newDir {
					registered.Store(true)
				}
				if path == marker {
					// Walk visits a-child.txt before this directory callback.
					completed <- struct{}{}
				}
				return nil
			}
			ctx, cancel := context.WithCancel(t.Context())
			require.NoError(t, fw.start(ctx, backend, backend.events, backend.errors))
			t.Cleanup(func() {
				cancel()
				waitStartupExit(t, fw.done)
			})
			require.NoError(t, os.Mkdir(newDir, 0700))
			require.NoError(t, os.Mkdir(transient, 0700))
			require.NoError(t, os.WriteFile(child, []byte("x"), 0600))
			require.NoError(t, os.Mkdir(marker, 0700))
			rescan.Store(true)
			backend.events <- fsnotify.Event{Name: newDir, Op: fsnotify.Create}
			select {
			case <-completed:
			case <-time.After(2 * time.Second):
				t.Fatal("a vanished earlier sibling prevented registration of the new directory")
			}

			require.True(t, registered.Load())
			require.Equal(t, FileChanges{{Path: child, ChangeType: FileCreated}}, fw.GetFileChanges())
			require.Empty(t, fw.initialFiles, "newly discovered files must not extend the fixed inventory")
		})
	}
}

func TestNewWatcher_DirectoryDiscoveryScansOnlyCreatedSubtrees(t *testing.T) {
	fw, backend := startupFixture(t)
	for i := range 24 {
		dir := filepath.Join(fw.root, fmt.Sprintf("existing-%d", i))
		require.NoError(t, os.Mkdir(dir, 0700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "initial.txt"), []byte("x"), 0600))
	}
	for i := range 3 {
		marker := filepath.Join(fw.root, fmt.Sprintf("marker-%d.txt", i))
		require.NoError(t, os.WriteFile(marker, []byte("x"), 0600))
	}
	var mu sync.Mutex
	adds := make(map[string]int)
	backend.add = func(path string) error {
		mu.Lock()
		defer mu.Unlock()
		adds[path]++
		return nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	require.NoError(t, fw.start(ctx, backend, backend.events, backend.errors))
	t.Cleanup(func() {
		cancel()
		waitStartupExit(t, fw.done)
	})
	for i := range 3 {
		marker := filepath.Join(fw.root, fmt.Sprintf("marker-%d.txt", i))
		dir := filepath.Join(fw.root, fmt.Sprintf("new-%d", i))
		child := filepath.Join(dir, "child.txt")
		require.NoError(t, os.Mkdir(dir, 0700))
		require.NoError(t, os.WriteFile(child, []byte("x"), 0600))
		backend.events <- fsnotify.Event{Name: dir, Op: fsnotify.Create}
		backend.events <- fsnotify.Event{Name: marker, Op: fsnotify.Write}
		require.Eventually(t, func() bool {
			fw.mu.Lock()
			defer fw.mu.Unlock()
			return fw.fileChanges.Modified[marker]
		}, 2*time.Second, time.Millisecond)
		require.Contains(t, fw.GetFileChanges(), FileChange{Path: child, ChangeType: FileCreated})
		mu.Lock()
		require.Equal(t, 1, adds[fw.root], "sequential creates must not re-register the root")
		for path, count := range adds {
			require.Equal(t, 1, count, "directory registered more than once: %s", path)
		}
		mu.Unlock()
	}
}

func TestNewWatcher_DirectoryDiscoveryCoalescesQueuedAncestors(t *testing.T) {
	for _, parentFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("parent first %v", parentFirst), func(t *testing.T) {
			fw, backend := startupFixture(t)
			marker := filepath.Join(fw.root, "marker.txt")
			require.NoError(t, os.WriteFile(marker, []byte("x"), 0600))
			blocked := filepath.Join(fw.root, "blocked")
			entered, release := make(chan struct{}), make(chan struct{})
			var enterOnce, releaseOnce sync.Once
			var mu sync.Mutex
			adds := make(map[string]int)
			backend.add = func(path string) error {
				mu.Lock()
				adds[path]++
				mu.Unlock()
				if path == blocked {
					enterOnce.Do(func() { close(entered) })
					<-release
				}
				return nil
			}
			ctx, cancel := context.WithCancel(t.Context())
			require.NoError(t, fw.start(ctx, backend, backend.events, backend.errors))
			t.Cleanup(func() {
				cancel()
				releaseOnce.Do(func() { close(release) })
				waitStartupExit(t, fw.done)
			})
			require.NoError(t, os.Mkdir(blocked, 0700))
			backend.events <- fsnotify.Event{Name: blocked, Op: fsnotify.Create}
			waitStartupExit(t, entered)
			parent := filepath.Join(fw.root, "parent")
			child := filepath.Join(parent, "child")
			require.NoError(t, os.MkdirAll(child, 0700))
			file := filepath.Join(child, "created.txt")
			require.NoError(t, os.WriteFile(file, []byte("x"), 0600))
			paths := []string{child, parent, child}
			if parentFirst {
				paths = []string{parent, child, parent}
			}
			for _, path := range paths {
				backend.events <- fsnotify.Event{Name: path, Op: fsnotify.Create}
			}
			backend.events <- fsnotify.Event{Name: marker, Op: fsnotify.Write}
			require.Eventually(t, func() bool {
				fw.mu.Lock()
				defer fw.mu.Unlock()
				return fw.fileChanges.Modified[marker]
			}, 2*time.Second, time.Millisecond, "consumer must process queued subtrees during blocked Add")
			releaseOnce.Do(func() { close(release) })
			require.Contains(t, fw.GetFileChanges(), FileChange{Path: file, ChangeType: FileCreated})
			mu.Lock()
			defer mu.Unlock()
			require.Equal(t, map[string]int{fw.root: 1, blocked: 1, parent: 1, child: 1}, adds)
		})
	}
}
func TestWatchRecursive_PreservesNonTransientErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		atRoot bool
		fault  error
		cancel bool
	}{
		{name: "missing root Add", atRoot: true, fault: os.ErrNotExist},
		{name: "root permission", atRoot: true, fault: os.ErrPermission},
		{name: "child permission", fault: os.ErrPermission},
		{name: "extant child backend NotExist", fault: os.ErrNotExist},
		{name: "child backend failure", fault: errors.New("backend failure")},
		{name: "permission during cancellation", fault: os.ErrPermission, cancel: true},
		{name: "backend failure during cancellation", fault: errors.New("backend failure"), cancel: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fw, backend := startupFixture(t)
			child := filepath.Join(fw.root, "child")
			require.NoError(t, os.Mkdir(child, 0700))
			target := child
			if test.atRoot {
				target = fw.root
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			backend.add = func(path string) error {
				if path == target {
					if test.cancel {
						cancel()
					}
					return &os.PathError{Op: "Add", Path: path, Err: test.fault}
				}
				return nil
			}
			err := fw.watchRecursive(ctx, fw.root, backend)
			require.ErrorIs(t, err, test.fault)
			if test.cancel {
				require.ErrorIs(t, err, context.Canceled)
			}
		})
	}
}

func TestWatchRecursive_PreservesCancellationDuringDisappearance(t *testing.T) {
	fw, backend := startupFixture(t)
	child := filepath.Join(fw.root, "child")
	require.NoError(t, os.Mkdir(child, 0700))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	backend.add = func(path string) error {
		if path == child {
			cancel()
			if err := os.Remove(child); err != nil {
				return err
			}
			return &os.PathError{Op: "Add", Path: path, Err: os.ErrNotExist}
		}
		return nil
	}
	err := fw.watchRecursive(ctx, fw.root, backend)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestWalkTracked_PreservesRootAndPermissionErrors(t *testing.T) {
	fw, _ := startupFixture(t)
	visit := func(string, os.FileInfo) error { return nil }
	require.ErrorIs(t, fw.walkTracked(t.Context(), filepath.Join(fw.root, "missing"), visit), os.ErrNotExist)
	child := filepath.Join(fw.root, "child")
	require.NoError(t, os.Mkdir(child, 0700))
	require.ErrorIs(t, fw.walkTracked(t.Context(), fw.root, func(path string, _ os.FileInfo) error {
		if path == child {
			return os.ErrPermission
		}
		return nil
	}), os.ErrPermission)
}
