// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package async

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProgress_SetProgressWithContext(t *testing.T) {
	t.Run("delivers update", func(t *testing.T) {
		progress := NewProgress[string]()
		sent := make(chan error, 1)
		go func() { sent <- progress.SetProgressWithContext(t.Context(), "update") }()
		require.Equal(t, "update", <-progress.Progress())
		require.NoError(t, <-sent)
		progress.Done()
	})

	t.Run("cancels without a receiver", func(t *testing.T) {
		progress := NewProgress[string]()
		ctx := newBlockingCancelContext(t.Context())
		sent := make(chan error, 1)
		go func() { sent <- progress.SetProgressWithContext(ctx, "update") }()
		<-ctx.checked
		ctx.cancel()
		require.ErrorIs(t, <-sent, context.Canceled)
		progress.Done()
	})

	t.Run("already canceled does not deliver", func(t *testing.T) {
		progress := NewNoopProgress[string]()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		require.ErrorIs(t, progress.SetProgressWithContext(ctx, "update"), context.Canceled)
		progress.Done()
	})
}

type blockingCancelContext struct {
	context.Context
	checked chan struct{}
	done    chan struct{}
	once    sync.Once
}

func newBlockingCancelContext(parent context.Context) *blockingCancelContext {
	return &blockingCancelContext{
		Context: parent,
		checked: make(chan struct{}),
		done:    make(chan struct{}),
	}
}

func (c *blockingCancelContext) Done() <-chan struct{} {
	return c.done
}

func (c *blockingCancelContext) Err() error {
	select {
	case <-c.done:
		return context.Canceled
	default:
		c.once.Do(func() { close(c.checked) })
		return nil
	}
}

func (c *blockingCancelContext) cancel() {
	close(c.done)
}
