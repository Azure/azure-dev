// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriterMutiplexer(t *testing.T) {
	w := &writerMultiplexer{}

	var buf1 bytes.Buffer
	var buf2 bytes.Buffer
	var buf3 bytes.Buffer

	w.AddWriter(&buf1)
	w.AddWriter(&buf2)

	_, err := w.Write([]byte("hello\n"))
	require.NoError(t, err)

	w.AddWriter(&buf3)
	w.RemoveWriter(&buf2)

	_, err = w.Write([]byte("world\n"))
	require.NoError(t, err)

	require.Equal(t, "hello\nworld\n", buf1.String())
	require.Equal(t, "hello\n", buf2.String())
	require.Equal(t, "world\n", buf3.String())
}

func TestWriterFunc_Implements_IOWriter(t *testing.T) {
	var captured []byte
	wf := writerFunc(func(p []byte) (int, error) {
		captured = append(captured, p...)
		return len(p), nil
	})

	// Prove it satisfies io.Writer
	var w io.Writer = wf
	n, err := w.Write([]byte("hello"))
	require.NoError(t, err)
	require.Equal(t, 5, n)
	require.Equal(t, []byte("hello"), captured)
}

func TestWriterFunc_PropagatesError(t *testing.T) {
	expectedErr := errors.New("broken writer")
	wf := writerFunc(func(p []byte) (int, error) {
		return 0, expectedErr
	})

	_, err := wf.Write([]byte("data"))
	require.ErrorIs(t, err, expectedErr)
}

func TestWriterMultiplexer_WriteToEmpty(t *testing.T) {
	wm := &writerMultiplexer{}

	// Writing to a multiplexer with no writers should succeed silently
	n, err := wm.Write([]byte("hello"))
	require.NoError(t, err)
	require.Equal(t, 0, n) // no writers => last n from loop is 0
}

func TestWriterMultiplexer_ErrorPropagation(t *testing.T) {
	expectedErr := errors.New("writer error")
	var good bytes.Buffer

	wm := &writerMultiplexer{}
	wm.AddWriter(&good)
	wm.AddWriter(writerFunc(func(p []byte) (int, error) {
		return 0, expectedErr
	}))

	_, err := wm.Write([]byte("data"))
	require.ErrorIs(t, err, expectedErr)
	// First writer should have received the data before the error
	require.Equal(t, "data", good.String())
}

func TestWriterMultiplexer_RemoveNonExistent(t *testing.T) {
	var buf bytes.Buffer
	wm := &writerMultiplexer{}
	wm.AddWriter(&buf)

	// Removing a writer not in the list should be a safe no-op
	var other bytes.Buffer
	wm.RemoveWriter(&other)

	// Original writer should still work
	_, err := wm.Write([]byte("still works"))
	require.NoError(t, err)
	require.Equal(t, "still works", buf.String())
}

func TestWriterMultiplexer_ConcurrentWriteAndModify(t *testing.T) {
	wm := &writerMultiplexer{}
	var buf bytes.Buffer
	wm.AddWriter(&buf)

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	// Concurrent writes
	for range goroutines {
		go func() {
			defer wg.Done()
			_, _ = wm.Write([]byte("x"))
		}()
	}

	// Concurrent add/remove
	for range goroutines {
		go func() {
			defer wg.Done()
			var tmp bytes.Buffer
			wm.AddWriter(&tmp)
			wm.RemoveWriter(&tmp)
		}()
	}

	wg.Wait()
	// Should not panic — we're testing thread safety
}
