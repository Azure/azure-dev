// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStableEventInvocationGate_CanceledWaiterDoesNotRun(t *testing.T) {
	var gate stableEventInvocationGate
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- gate.run(t.Context(), nil, "event", func() error {
			close(firstStarted)
			<-releaseFirst
			return nil
		})
	}()

	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first invocation did not start")
	}

	secondCtx, cancelSecond := context.WithCancel(t.Context())
	secondRan := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- gate.run(secondCtx, nil, "event", func() error {
			close(secondRan)
			return nil
		})
	}()
	require.Eventually(t, func() bool {
		gate.mu.Lock()
		defer gate.mu.Unlock()

		for _, slot := range gate.slots {
			if slot.refs == 2 {
				return true
			}
		}
		return false
	}, time.Second, 10*time.Millisecond)
	cancelSecond()

	select {
	case err := <-secondDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("canceled waiter did not stop")
	}
	select {
	case <-secondRan:
		t.Fatal("canceled waiter ran")
	default:
	}

	close(releaseFirst)
	require.NoError(t, <-firstDone)
	require.NoError(t, gate.run(t.Context(), nil, "event", func() error {
		return nil
	}))
}

func TestStableEventInvocationGate_AllowsDifferentKeysConcurrently(t *testing.T) {
	var gate stableEventInvocationGate
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- gate.run(t.Context(), nil, "first", func() error {
			close(firstStarted)
			<-releaseFirst
			return nil
		})
	}()

	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first invocation did not start")
	}

	secondRan := make(chan struct{})
	require.NoError(t, gate.run(t.Context(), nil, "second", func() error {
		close(secondRan)
		return nil
	}))
	select {
	case <-secondRan:
	default:
		t.Fatal("different-key invocation did not run")
	}

	close(releaseFirst)
	require.NoError(t, <-firstDone)
}
