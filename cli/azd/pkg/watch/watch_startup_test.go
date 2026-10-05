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
		t.Fatal("initial registration did not reach injected backend backpressure")
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
