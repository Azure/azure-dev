// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package watch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/require"
)

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
			sibling := filepath.Join(newDir, "b-sibling")
			child := filepath.Join(sibling, "a-child.txt")
			marker := filepath.Join(newDir, "z-complete")
			var registered atomic.Bool
			completed := make(chan struct{}, 1)
			backend.add = func(path string) error {
				if pathWithin(newDir, path) {
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
				if path == sibling {
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
			require.NoError(t, os.MkdirAll(transient, 0700))
			require.NoError(t, os.Mkdir(sibling, 0700))
			require.NoError(t, os.WriteFile(child, []byte("x"), 0600))
			require.NoError(t, os.Mkdir(marker, 0700))
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

func TestDirectoryQueue_DeduplicatesPendingSubtrees(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "new")
	child := filepath.Join(parent, "child")
	sibling := filepath.Join(root, "new-sibling")
	for _, paths := range [][]string{
		{parent, child, parent, sibling},
		{child, parent, sibling, child},
	} {
		queue := &directoryQueue{pending: map[string]struct{}{}, ready: make(chan struct{}, 1)}
		for _, path := range paths {
			queue.add(path)
		}
		require.Len(t, queue.ready, 1, "wakeups must coalesce without blocking the event consumer")
		<-queue.ready
		require.Equal(t, []string{parent, sibling}, queue.take())
		require.Empty(t, queue.pending, "completed discoveries must not accumulate")
		queue.add(child)
		require.Len(t, queue.ready, 1, "later generations must be scheduled again")
		require.Equal(t, []string{child}, queue.take())
	}
}

func TestNewWatcher_DirectoryStreamDoesNotRevisitEstablishedTree(t *testing.T) {
	fw, backend := startupFixture(t)
	for i := range 20 {
		require.NoError(t, os.Mkdir(filepath.Join(fw.root, fmt.Sprintf("existing-%02d", i)), 0700))
	}
	var calls atomic.Int32
	added := make(chan string, 64)
	backend.add = func(path string) error {
		calls.Add(1)
		added <- path
		return nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	require.NoError(t, fw.start(ctx, backend, backend.events, backend.errors))
	t.Cleanup(func() {
		cancel()
		waitStartupExit(t, fw.done)
	})
	require.EqualValues(t, 21, calls.Load())
	for range 21 {
		<-added
	}
	for i := range 20 {
		dir := filepath.Join(fw.root, fmt.Sprintf("new-%02d", i))
		require.NoError(t, os.Mkdir(dir, 0700))
		backend.events <- fsnotify.Event{Name: dir, Op: fsnotify.Create}
		select {
		case path := <-added:
			require.Equal(t, dir, path, "dynamic registration must start at the discovered directory, not root")
		case <-time.After(2 * time.Second):
			t.Fatal("new subtree was not registered")
		}
	}
	cancel()
	waitStartupExit(t, fw.done)
	require.EqualValues(t, 41, calls.Load(), "continuous discoveries must not revisit established directories")
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
