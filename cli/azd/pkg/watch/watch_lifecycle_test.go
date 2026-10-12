// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package watch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/require"
)

type lifecycleReply struct {
	reply chan error
}

// nativeLifecycleBackend models unconditional Add replies and a Close check
// outside serialization: concurrent callers can pass the check, but only the
// first shutdown request receives a reply. Rescue is test-failure cleanup only.
type nativeLifecycleBackend struct {
	events      chan fsnotify.Event
	errors      chan error
	addQueued   chan *lifecycleReply
	closeQueued chan *lifecycleReply
	closeGate   chan struct{}
	gateOnce    sync.Once
	closed      atomic.Bool
	closeCalls  atomic.Int32
	mu          sync.Mutex
	pending     []*lifecycleReply
	addFailure  error
}

func (b *nativeLifecycleBackend) request() *lifecycleReply {
	request := &lifecycleReply{reply: make(chan error, 1)}
	b.mu.Lock()
	b.pending = append(b.pending, request)
	b.mu.Unlock()
	return request
}

func (b *nativeLifecycleBackend) Add(string) error {
	if b.addFailure != nil {
		return b.addFailure
	}
	request := b.request()
	b.addQueued <- request
	// Native Add does not select on closure/cancellation while awaiting reply.
	return <-request.reply
}

func (b *nativeLifecycleBackend) Close() error {
	if b.closed.Load() {
		return nil
	}
	request := b.request()
	b.closeCalls.Add(1)
	b.closeQueued <- request
	<-b.closeGate
	if b.closed.CompareAndSwap(false, true) {
		close(b.events)
		close(b.errors)
		request.reply <- nil
	}
	// A queued Add or second concurrent Close gets no reply from shutdown.
	return <-request.reply
}

func (b *nativeLifecycleBackend) releaseClose() {
	b.gateOnce.Do(func() { close(b.closeGate) })
}

func (b *nativeLifecycleBackend) rescue() {
	b.releaseClose()
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, request := range b.pending {
		select {
		case request.reply <- errors.New("test cleanup rescued an abandoned native reply"):
		default:
		}
	}
}

func nativeLifecycleFixture(t *testing.T) (*fileWatcher, *nativeLifecycleBackend) {
	t.Helper()
	fw, _ := startupFixture(t)
	backend := &nativeLifecycleBackend{
		events:      make(chan fsnotify.Event, 50),
		errors:      make(chan error),
		addQueued:   make(chan *lifecycleReply, 4),
		closeQueued: make(chan *lifecycleReply, 4),
		closeGate:   make(chan struct{}),
	}
	t.Cleanup(backend.rescue)
	return fw, backend
}

func TestNewWatcher_CancellationPreservesPendingNativeAddReply(t *testing.T) {
	fw, backend := nativeLifecycleFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- fw.start(ctx, backend, backend.events, backend.errors) }()
	var add *lifecycleReply
	select {
	case add = <-backend.addQueued:
	case <-time.After(2 * time.Second):
		t.Fatal("native Add was not queued")
	}
	cancel()
	select {
	case <-backend.closeQueued:
		backend.releaseClose()
		// Shutdown drops the outstanding Add reply in the native backend.
		select {
		case <-finished:
			t.Fatal("shutdown must not discard a pending Add reply")
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Close preceded pending Add completion and left startup blocked")
		}
	case <-time.After(50 * time.Millisecond):
	}
	add.reply <- nil
	select {
	case <-backend.closeQueued:
		backend.releaseClose()
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown was not queued after Add completion")
	}
	select {
	case err := <-finished:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("startup did not join orderly cancellation")
	}
	waitStartupExit(t, fw.done)
	require.EqualValues(t, 1, backend.closeCalls.Load())
}

func TestNewWatcher_RegistrationFailureSerializesNativeClose(t *testing.T) {
	fw, backend := nativeLifecycleFixture(t)
	failure := errors.New("native Add failed")
	backend.addFailure = failure
	finished := make(chan error, 1)
	go func() { finished <- fw.start(t.Context(), backend, backend.events, backend.errors) }()
	select {
	case <-backend.closeQueued:
	case <-time.After(2 * time.Second):
		t.Fatal("failed registration did not queue shutdown")
	}
	select {
	case <-backend.closeQueued:
		backend.releaseClose()
		t.Fatal("concurrent Close calls passed the native pre-lock closed check")
	case <-time.After(50 * time.Millisecond):
	}
	backend.releaseClose()
	select {
	case err := <-finished:
		require.ErrorIs(t, err, failure)
	case <-time.After(2 * time.Second):
		t.Fatal("failed registration did not join backend shutdown")
	}
	waitStartupExit(t, fw.done)
	require.EqualValues(t, 1, backend.closeCalls.Load())
}

func TestNewWatcher_DynamicNativeAddKeepsConsumerResponsive(t *testing.T) {
	fw, backend := nativeLifecycleFixture(t)
	file := filepath.Join(fw.root, "existing.txt")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0600))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan error, 1)
	go func() { started <- fw.start(ctx, backend, backend.events, backend.errors) }()
	select {
	case initial := <-backend.addQueued:
		initial.reply <- nil
	case <-time.After(2 * time.Second):
		t.Fatal("initial Add was not queued")
	}
	select {
	case err := <-started:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("initial registration did not complete")
	}
	child := filepath.Join(fw.root, "child")
	require.NoError(t, os.Mkdir(child, 0700))
	backend.events <- fsnotify.Event{Name: child, Op: fsnotify.Create}
	var pending *lifecycleReply
	select {
	case pending = <-backend.addQueued:
	case <-time.After(2 * time.Second):
		t.Fatal("dynamic registration was not queued")
	}
	stopProducer := make(chan struct{})
	produced := make(chan struct{})
	t.Cleanup(func() {
		close(stopProducer)
		<-produced
		cancel()
	})
	go func() {
		defer close(produced)
		for range 51 {
			select {
			case backend.events <- fsnotify.Event{Name: file, Op: fsnotify.Write}:
			case <-stopProducer:
				return
			}
		}
	}()
	select {
	case <-produced:
	case <-time.After(2 * time.Second):
		t.Fatal("pending dynamic native Add blocked the sole event consumer")
	}
	// Cancel before replying: the consumer must remain available until this
	// unconditional native reply wait completes, then shutdown may begin.
	cancel()
	pending.reply <- nil
	select {
	case <-backend.closeQueued:
		backend.releaseClose()
	case <-time.After(2 * time.Second):
		t.Fatal("dynamic registration did not finish before shutdown")
	}
	waitStartupExit(t, fw.done)
	require.EqualValues(t, 1, backend.closeCalls.Load())
}
