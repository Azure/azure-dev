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
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/ignore"
	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/require"
)

type startupBackend struct {
	events chan fsnotify.Event
	errors chan error
	closed chan struct{}
	once   sync.Once
	add    func(string) error
}

func (b *startupBackend) Add(path string) error {
	return b.add(path)
}

func (b *startupBackend) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

func TestNewWatcher_ConsumesEventsDuringInitialRegistration(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "existing.txt")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0600))
	require.NoError(t, os.Mkdir(filepath.Join(root, "child"), 0700))
	ctx, cancel := context.WithCancel(t.Context())
	backend := &startupBackend{
		events: make(chan fsnotify.Event, 50),
		errors: make(chan error),
		closed: make(chan struct{}),
	}
	filled := make(chan struct{})
	backend.add = func(path string) error {
		if path == root {
			for i := range 51 {
				if i == 50 {
					close(filled)
				}
				select {
				case backend.events <- fsnotify.Event{Name: file, Op: fsnotify.Write}:
				case <-backend.closed:
					return fmt.Errorf("backend closed during Add")
				}
			}
		}
		return nil
	}
	type result struct {
		watcher *fileWatcher
		err     error
	}
	finished := make(chan result, 1)
	go func() {
		w, err := newFileWatcher(ctx, root, backend, backend.events, backend.errors)
		finished <- result{w, err}
	}()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, backend.Close())
	})

	select {
	case <-filled:
	case <-time.After(2 * time.Second):
		t.Fatal("initial registration did not reach injected backend event queue pressure")
	}
	select {
	case result := <-finished:
		require.NoError(t, result.err)
		require.NotNil(t, result.watcher)
		cancel()
		waitStartupExit(t, result.watcher.done)
	case <-time.After(2 * time.Second):
		t.Fatal("initial registration must finish while the backend emits more events than its buffer holds")
	}
}

func startupFixture(t *testing.T) (*fileWatcher, *startupBackend) {
	t.Helper()
	root := t.TempDir()
	matcher, err := ignore.NewMatcher(root)
	require.NoError(t, err)
	backend := &startupBackend{
		events: make(chan fsnotify.Event, 50),
		errors: make(chan error),
		closed: make(chan struct{}),
		add:    func(string) error { return nil },
	}
	t.Cleanup(func() { require.NoError(t, backend.Close()) })
	return &fileWatcher{
		root:          root,
		ignoreMatcher: matcher,
		initialFiles:  map[string]struct{}{},
		fileChanges: &fileChanges{
			Created:  map[string]bool{},
			Modified: map[string]bool{},
			Deleted:  map[string]bool{},
		},
	}, backend
}

func waitStartupExit(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watch consumer did not exit")
	}
}

func TestNewWatcher_InitialRegistrationFailureJoinsConsumer(t *testing.T) {
	fw, backend := startupFixture(t)
	failure := errors.New("injected Add failure")
	backend.add = func(string) error { return failure }
	err := fw.start(t.Context(), backend, backend.events, backend.errors)
	require.ErrorIs(t, err, failure)
	waitStartupExit(t, fw.done)
	waitStartupExit(t, backend.closed)
}

func TestNewWatcher_CancellationDrainsInitialAddBeforeClose(t *testing.T) {
	fw, backend := startupFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	backend.add = func(string) error {
		cancel()
		select {
		case <-backend.closed:
			return errors.New("backend closed before Add completed")
		default:
		}
		for range 51 {
			backend.events <- fsnotify.Event{Op: fsnotify.Write}
		}
		return ctx.Err()
	}
	err := fw.start(ctx, backend, backend.events, backend.errors)
	require.ErrorIs(t, err, context.Canceled)
	waitStartupExit(t, fw.done)
	waitStartupExit(t, backend.closed)
}

func TestNewWatcher_ClosedChannelsExitConsumer(t *testing.T) {
	fw, backend := startupFixture(t)
	require.NoError(t, fw.start(t.Context(), backend, backend.events, backend.errors))
	close(backend.events)
	close(backend.errors)
	waitStartupExit(t, fw.done)
	waitStartupExit(t, backend.closed)
}

func TestNewWatcher_CanceledInventoryDoesNotStartConsumer(t *testing.T) {
	fw, backend := startupFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	backend.add = func(string) error {
		t.Error("a canceled inventory must not register any watches")
		return nil
	}
	err := fw.start(ctx, backend, backend.events, backend.errors)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, fw.done)
	waitStartupExit(t, backend.closed)
}

func TestNewWatcher_CanceledContextReturnsNilWatcher(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	watcher, err := NewWatcher(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, watcher)
}

func TestNewWatcher_DiscoversFilesCreatedDuringRegistration(t *testing.T) {
	fw, backend := startupFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	child := filepath.Join(fw.root, "child")
	require.NoError(t, os.Mkdir(child, 0700))
	file := filepath.Join(child, "new.txt")
	backend.add = func(path string) error {
		if path == fw.root {
			return os.WriteFile(file, []byte("x"), 0600)
		}
		return nil
	}
	require.NoError(t, fw.start(ctx, backend, backend.events, backend.errors))
	require.Empty(t, fw.initialFiles, "the inventory must be fixed before any Add call")
	require.Equal(t, FileChanges{{Path: file, ChangeType: FileCreated}}, fw.GetFileChanges())
	cancel()
	waitStartupExit(t, fw.done)
}

func TestNewWatcher_DiscoversFilesDeletedDuringRegistration(t *testing.T) {
	for _, removeDirectory := range []bool{false, true} {
		name := "file"
		if removeDirectory {
			name = "directory"
		}
		t.Run(name, func(t *testing.T) {
			fw, backend := startupFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			child := filepath.Join(fw.root, "child")
			require.NoError(t, os.Mkdir(child, 0700))
			file := filepath.Join(child, "existing.txt")
			require.NoError(t, os.WriteFile(file, []byte("x"), 0600))
			ignored := filepath.Join(fw.root, "ignored.txt")
			require.NoError(t, os.WriteFile(ignored, []byte("x"), 0600))
			require.NoError(t, os.WriteFile(filepath.Join(fw.root, ".gitignore"), []byte("ignored.txt\n"), 0600))
			matcher, err := ignore.NewMatcher(fw.root)
			require.NoError(t, err)
			fw.ignoreMatcher = matcher
			backend.add = func(path string) error {
				if path != fw.root {
					return nil
				}
				if err := os.Remove(ignored); err != nil {
					return err
				}
				if removeDirectory {
					return os.RemoveAll(child)
				}
				return os.Remove(file)
			}
			require.NoError(t, fw.start(ctx, backend, backend.events, backend.errors))
			require.Contains(t, fw.initialFiles, file)
			require.Equal(t, FileChanges{{Path: file, ChangeType: FileDeleted}}, fw.GetFileChanges())
			cancel()
			waitStartupExit(t, fw.done)
		})
	}
}

func TestNewWatcher_CancellationDrainsDynamicAddBeforeClose(t *testing.T) {
	fw, backend := startupFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	child := filepath.Join(fw.root, "new-directory")
	backend.add = func(path string) error {
		if path != child {
			return nil
		}
		cancel()
		select {
		case <-backend.closed:
			return errors.New("backend closed before dynamic Add completed")
		default:
		}
		for range 51 {
			backend.events <- fsnotify.Event{Op: fsnotify.Write}
		}
		return ctx.Err()
	}
	require.NoError(t, fw.start(ctx, backend, backend.events, backend.errors))
	require.NoError(t, os.Mkdir(child, 0700))
	backend.events <- fsnotify.Event{Name: child, Op: fsnotify.Create}
	waitStartupExit(t, fw.done)
	waitStartupExit(t, backend.closed)
}

func TestFileChanges_SlowStatDoesNotBlockOrPruneConcurrentEvents(t *testing.T) {
	fw, _ := startupFixture(t)
	file := filepath.Join(fw.root, "created.txt")
	fw.mu.Lock()
	fw.trackFileEventLocked(fsnotify.Event{Name: file, Op: fsnotify.Create})
	fw.mu.Unlock()
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	result := make(chan fileChanges, 1)
	go func() {
		result <- fw.snapshotFileChanges(func(string) (os.FileInfo, error) {
			close(started)
			<-release
			return nil, os.ErrNotExist
		})
	}()
	waitStartupExit(t, started)
	eventDone := make(chan struct{})
	go func() {
		fw.mu.Lock()
		fw.trackFileEventLocked(fsnotify.Event{Name: file, Op: fsnotify.Create})
		fw.mu.Unlock()
		close(eventDone)
	}()
	waitStartupExit(t, eventDone)
	release <- struct{}{}
	select {
	case snapshot := <-result:
		require.Empty(t, snapshot.Created, "the earlier report excludes its missing path")
	case <-time.After(2 * time.Second):
		t.Fatal("snapshot did not finish after filesystem I/O completed")
	}
	fw.mu.Lock()
	require.Contains(t, fw.fileChanges.Created, file, "a stale stat must not prune a newer Create")
	fw.mu.Unlock()
	require.Empty(t, fw.GetFileChanges())
	fw.mu.Lock()
	require.Empty(t, fw.fileChanges.Created, "a subsequent quiescent snapshot still reclaims missing paths")
	fw.mu.Unlock()
}

func TestNewWatcher_ReconcilesDeletionBeforeInitialRegistration(t *testing.T) {
	for _, nested := range []bool{false, true} {
		name := "initial file"
		if nested {
			name = "initial nested directory"
		}
		t.Run(name, func(t *testing.T) {
			fw, backend := startupFixture(t)
			parent := fw.root
			if nested {
				parent = filepath.Join(fw.root, "nested")
				require.NoError(t, os.Mkdir(parent, 0700))
			}
			file := filepath.Join(parent, "initial.txt")
			require.NoError(t, os.WriteFile(file, []byte("x"), 0600))
			backend.add = func(path string) error {
				if path != fw.root {
					return nil
				}
				if err := os.Remove(file); err != nil {
					return err
				}
				if nested {
					return os.Remove(parent)
				}
				return nil
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			require.NoError(t, fw.start(ctx, backend, backend.events, backend.errors))
			t.Cleanup(func() {
				cancel()
				waitStartupExit(t, fw.done)
			})
			require.Equal(t, FileChanges{{Path: file, ChangeType: FileDeleted}}, fw.GetFileChanges(),
				"a missing backend Remove during registration must not lose an initial deletion")
			require.Equal(t, map[string]struct{}{file: {}}, fw.initialFiles,
				"registration reconciliation must not mutate initial provenance")
			fw.mu.Lock()
			fw.trackFileEventLocked(fsnotify.Event{Name: file, Op: fsnotify.Remove})
			fw.mu.Unlock()
			require.Equal(t, FileChanges{{Path: file, ChangeType: FileDeleted}}, fw.GetFileChanges(),
				"a later real Remove must preserve the reconciled deletion")
		})
	}
}

func TestNewWatcher_StartupReconciliationHonorsIgnores(t *testing.T) {
	fw, backend := startupFixture(t)
	ignoreFile := filepath.Join(fw.root, ".azdxignore")
	require.NoError(t, os.WriteFile(ignoreFile, []byte("*.log\nignored/\n"), 0600))
	matcher, err := ignore.NewMatcher(fw.root)
	require.NoError(t, err)
	fw.ignoreMatcher = matcher
	file := filepath.Join(fw.root, "ignored.log")
	dir := filepath.Join(fw.root, "ignored")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0600))
	require.NoError(t, os.Mkdir(dir, 0700))
	child := filepath.Join(dir, "child.txt")
	require.NoError(t, os.WriteFile(child, []byte("x"), 0600))
	backend.add = func(path string) error {
		if path == fw.root {
			if err := os.Remove(file); err != nil {
				return err
			}
			if err := os.Remove(child); err != nil {
				return err
			}
			return os.Remove(dir)
		}
		return nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	require.NoError(t, fw.start(ctx, backend, backend.events, backend.errors))
	t.Cleanup(func() {
		cancel()
		waitStartupExit(t, fw.done)
	})
	require.Empty(t, fw.GetFileChanges(), "ignored startup deletions must not leak into changes")
	require.NotContains(t, fw.initialFiles, child, "ignored directories must not be inventoried")
}

func TestNewWatcher_StartupReconciliationPreservesFileIgnoreSemantics(t *testing.T) {
	for _, directory := range []bool{false, true} {
		name := "initial file removed"
		if directory {
			name = "initial file replaced by directory"
		}
		t.Run(name, func(t *testing.T) {
			fw, backend := startupFixture(t)
			require.NoError(t, os.WriteFile(filepath.Join(fw.root, ".azdxignore"), []byte("ignored/\n"), 0600))
			matcher, err := ignore.NewMatcher(fw.root)
			require.NoError(t, err)
			fw.ignoreMatcher = matcher
			require.False(t, matcher.IsIgnored("ignored", false), "the original file is not ignored")
			require.True(t, matcher.IsIgnored("ignored", true), "the pattern ignores only directories")
			path := filepath.Join(fw.root, "ignored")
			require.NoError(t, os.WriteFile(path, []byte("original"), 0600))
			backend.add = func(parent string) error {
				if parent != fw.root {
					return nil
				}
				if _, exists := fw.initialFiles[path]; !exists {
					return errors.New("the original tracked file was not inventoried before Add")
				}
				if err := os.Remove(path); err != nil {
					return err
				}
				if directory {
					return os.Mkdir(path, 0700)
				}
				return nil
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			require.NoError(t, fw.start(ctx, backend, backend.events, backend.errors))
			t.Cleanup(func() {
				cancel()
				waitStartupExit(t, fw.done)
			})
			require.Contains(t, fw.initialFiles, path)
			for range 2 {
				require.Equal(t, FileChanges{{Path: path, ChangeType: FileDeleted}}, fw.GetFileChanges(),
					"a directory-only ignore must not suppress the original file deletion")
			}
		})
	}
}

func TestNewWatcher_StartupReconciliationRetainsOriginalDeletion(t *testing.T) {
	for _, directory := range []bool{true, false} {
		name := "initial file replaced by directory"
		if !directory {
			name = "initial file briefly recreated with observed Create"
		}
		t.Run(name, func(t *testing.T) {
			fw, backend := startupFixture(t)
			path := filepath.Join(fw.root, "initial.txt")
			require.NoError(t, os.WriteFile(path, []byte("original"), 0600))
			backend.add = func(parent string) error {
				if parent != fw.root {
					return nil
				}
				if err := os.Remove(path); err != nil {
					return err
				}
				if directory {
					return os.Mkdir(path, 0700)
				}
				if err := os.WriteFile(path, []byte("recreated"), 0600); err != nil {
					return err
				}
				// Inject the same accounting used by the consumer, without
				// depending on channel scheduling or an actual Remove event.
				fw.mu.Lock()
				fw.trackFileEventLocked(fsnotify.Event{Name: path, Op: fsnotify.Create})
				fw.mu.Unlock()
				return os.Remove(path)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			require.NoError(t, fw.start(ctx, backend, backend.events, backend.errors))
			t.Cleanup(func() {
				cancel()
				waitStartupExit(t, fw.done)
			})
			for range 2 {
				require.Equal(t, FileChanges{{Path: path, ChangeType: FileDeleted}}, fw.GetFileChanges(),
					"startup provenance must preserve the original deletion")
			}
			require.Empty(t, fw.fileChanges.Created)
			require.Empty(t, fw.fileChanges.Modified)
			require.Equal(t, map[string]struct{}{path: {}}, fw.initialFiles)
		})
	}
}

func TestReconcileInitialFiles_PreservesAccountingAndErrors(t *testing.T) {
	for _, test := range []struct {
		name     string
		fault    error
		cancel   bool
		created  bool
		wantType FileChangeType
	}{
		{name: "present modified", wantType: FileModified},
		{name: "missing modified", fault: os.ErrNotExist, wantType: FileDeleted},
		{name: "present recreated", created: true, wantType: FileCreated},
		{name: "missing recreated", fault: os.ErrNotExist, created: true, wantType: FileDeleted},
		{name: "permission", fault: os.ErrPermission, wantType: FileModified},
		{name: "backend lookup failure", fault: errors.New("injected lookup failure"), wantType: FileModified},
		{name: "canceled lookup", cancel: true, wantType: FileModified},
		{name: "missing and cancellation", fault: os.ErrNotExist, cancel: true, wantType: FileModified},
		{name: "permission and cancellation", fault: os.ErrPermission, cancel: true, wantType: FileModified},
	} {
		t.Run(test.name, func(t *testing.T) {
			fw, _ := startupFixture(t)
			path := filepath.Join(fw.root, "initial.txt")
			require.NoError(t, os.WriteFile(path, []byte("x"), 0600))
			info, err := os.Lstat(path)
			require.NoError(t, err)
			fw.initialFiles[path] = struct{}{}
			if test.created {
				fw.fileChanges.Created[path] = true
			} else {
				fw.fileChanges.Modified[path] = true
			}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			err = fw.reconcileInitialFiles(ctx, func(string) (os.FileInfo, error) {
				if test.cancel {
					cancel()
				}
				if test.fault != nil {
					return nil, &os.PathError{Op: "Lstat", Path: path, Err: test.fault}
				}
				return info, nil
			})
			if test.cancel {
				require.ErrorIs(t, err, context.Canceled)
			}
			if test.fault != nil && (!errors.Is(test.fault, os.ErrNotExist) || test.cancel) {
				require.ErrorIs(t, err, test.fault)
			} else if !test.cancel {
				require.NoError(t, err)
			}
			require.Equal(t, FileChanges{{Path: path, ChangeType: test.wantType}}, fw.GetFileChanges())
			require.Equal(t, map[string]struct{}{path: {}}, fw.initialFiles)
		})
	}
}

func TestReconcileInitialFiles_SlowStatPreservesConcurrentEvents(t *testing.T) {
	for _, test := range []struct {
		name     string
		samePath bool
		remove   bool
	}{
		{name: "unrelated create"},
		{name: "same path create", samePath: true},
		{name: "same path remove after create", samePath: true, remove: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fw, _ := startupFixture(t)
			path := filepath.Join(fw.root, "initial.txt")
			fw.initialFiles[path] = struct{}{}
			eventPath := path
			if !test.samePath {
				eventPath = filepath.Join(fw.root, "other.txt")
			}

			started := make(chan struct{})
			release := make(chan struct{})
			defer close(release)
			result := make(chan error, 1)
			go func() {
				result <- fw.reconcileInitialFiles(t.Context(), func(string) (os.FileInfo, error) {
					close(started)
					<-release
					return nil, os.ErrNotExist
				})
			}()
			waitStartupExit(t, started)
			eventDone := make(chan struct{})
			go func() {
				fw.mu.Lock()
				fw.trackFileEventLocked(fsnotify.Event{Name: eventPath, Op: fsnotify.Create})
				if test.remove {
					fw.trackFileEventLocked(fsnotify.Event{Name: eventPath, Op: fsnotify.Remove})
				}
				fw.mu.Unlock()
				close(eventDone)
			}()
			waitStartupExit(t, eventDone)
			release <- struct{}{}
			select {
			case err := <-result:
				require.NoError(t, err)
			case <-time.After(2 * time.Second):
				t.Fatal("startup reconciliation did not finish")
			}
			fw.mu.Lock()
			defer fw.mu.Unlock()
			require.Equal(t, !test.remove, fw.fileChanges.Created[eventPath])
			require.Equal(t, !test.samePath || test.remove, fw.fileChanges.Deleted[path])
			require.Nil(t, fw.startupRevisions, "startup generations must not persist")
		})
	}
}

func TestReconcileInitialFiles_RenameDuringLookup(t *testing.T) {
	for _, test := range []struct {
		name         string
		op           fsnotify.Op
		next         fsnotify.Op
		want         FileChangeType
		alsoModified bool
	}{
		{name: "pure rename", op: fsnotify.Rename, want: FileDeleted},
		{name: "rename and remove", op: fsnotify.Rename | fsnotify.Remove, want: FileDeleted},
		{name: "rename and create", op: fsnotify.Rename | fsnotify.Create, want: FileCreated},
		{name: "rename and write", op: fsnotify.Rename | fsnotify.Write, want: FileModified},
		{name: "rename then create", op: fsnotify.Rename, next: fsnotify.Create, want: FileCreated, alsoModified: true},
		{name: "rename then write", op: fsnotify.Rename, next: fsnotify.Write, want: FileModified},
	} {
		t.Run(test.name, func(t *testing.T) {
			fw, _ := startupFixture(t)
			path := filepath.Join(fw.root, "initial.txt")
			require.NoError(t, os.WriteFile(path, []byte("original"), 0600))
			fw.initialFiles[path] = struct{}{}
			started, lookup, lookedUp, release := make(chan struct{}), make(chan struct{}),
				make(chan struct{}), make(chan struct{})
			var lookupOnce, releaseOnce sync.Once
			t.Cleanup(func() {
				lookupOnce.Do(func() { close(lookup) })
				releaseOnce.Do(func() { close(release) })
			})
			result := make(chan error, 1)
			go func() {
				result <- fw.reconcileInitialFiles(t.Context(), func(name string) (os.FileInfo, error) {
					close(started)
					<-lookup
					info, err := os.Lstat(name)
					close(lookedUp)
					<-release
					return info, err
				})
			}()
			waitStartupExit(t, started)
			require.NoError(t, os.Rename(path, filepath.Join(fw.root, "renamed.txt")))
			lookupOnce.Do(func() { close(lookup) })
			waitStartupExit(t, lookedUp)
			if test.want != FileDeleted {
				require.NoError(t, os.WriteFile(path, []byte("replacement"), 0600))
			}
			eventDone := make(chan struct{})
			go func() {
				fw.mu.Lock()
				fw.trackFileEventLocked(fsnotify.Event{Name: path, Op: test.op})
				if test.next != 0 {
					fw.trackFileEventLocked(fsnotify.Event{Name: path, Op: test.next})
				}
				fw.mu.Unlock()
				close(eventDone)
			}()
			waitStartupExit(t, eventDone)
			releaseOnce.Do(func() { close(release) })
			select {
			case err := <-result:
				require.NoError(t, err)
			case <-time.After(2 * time.Second):
				t.Fatal("rename reconciliation did not finish")
			}
			expected := FileChanges{{Path: path, ChangeType: test.want}}
			if test.alsoModified {
				expected = append(expected, FileChange{Path: path, ChangeType: FileModified})
			}
			require.Equal(t, expected, fw.GetFileChanges())
			require.Nil(t, fw.startupRevisions)
		})
	}
}
