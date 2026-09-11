//go:build !race

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Excluded from race builds until ReadInput cancellation stops leaking its input goroutine (https://github.com/Azure/azure-dev/issues/9995).
func TestReadInput_ContextCancellationReturnsErrCancelled(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = r.Close()
		_ = w.Close()
	})

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // pre-cancel

	in := NewInput(io.Discard)
	done := make(chan error, 1)
	handlerCalled := make(chan struct{}, 1)
	go func() {
		done <- in.ReadInput(ctx, &InputConfig{Stdin: r}, func(args *KeyPressEventArgs) (bool, error) {
			select {
			case handlerCalled <- struct{}{}:
			default:
			}
			require.True(t, args.Cancelled)
			return true, nil
		})
	}()

	select {
	case gotErr := <-done:
		// Either the ctx.Done path fires first (returning ErrCancelled joined
		// with ctx.Err), or SetTermMode errors first (returning that err).
		// Both are acceptable; we just require termination and, when the
		// cancel path wins, verify ErrCancelled is part of the error chain.
		if gotErr != nil && errors.Is(gotErr, ErrCancelled) {
			require.ErrorIs(t, gotErr, context.Canceled)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReadInput did not return within 5s after ctx cancel")
	}
}
