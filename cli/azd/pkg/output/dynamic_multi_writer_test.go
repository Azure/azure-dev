// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"bytes"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDynamicMultiWriter_Default_DiscardsWhenNoWriters(t *testing.T) {
	t.Parallel()
	dmw := NewDynamicMultiWriter()
	n, err := dmw.Write([]byte("hello"))
	require.NoError(t, err)
	require.Equal(t, 5, n)
}

func TestDynamicMultiWriter_WriteFansOutToAll(t *testing.T) {
	t.Parallel()
	var a, b bytes.Buffer
	dmw := NewDynamicMultiWriter(&a, &b)
	n, err := dmw.Write([]byte("hello"))
	require.NoError(t, err)
	require.Equal(t, 5, n)
	require.Equal(t, "hello", a.String())
	require.Equal(t, "hello", b.String())
}

func TestDynamicMultiWriter_AddAndRemoveWriter(t *testing.T) {
	t.Parallel()
	var initial, added bytes.Buffer
	dmw := NewDynamicMultiWriter(&initial)
	dmw.AddWriter(&added)

	_, err := dmw.Write([]byte("x"))
	require.NoError(t, err)
	require.Equal(t, "x", initial.String())
	require.Equal(t, "x", added.String())

	dmw.RemoveWriter(&added)
	_, err = dmw.Write([]byte("y"))
	require.NoError(t, err)
	require.Equal(t, "xy", initial.String())
	require.Equal(t, "x", added.String(), "removed writer should not receive further writes")
}

func (errWriter) Write(_ []byte) (int, error) { return 0, errors.New("boom") }

func TestDynamicMultiWriter_ReturnsErrOnWriterFailure(t *testing.T) {
	t.Parallel()
	dmw := NewDynamicMultiWriter(errWriter{})
	_, err := dmw.Write([]byte("x"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "boom")
}

func TestDynamicMultiWriter_ConcurrentWrites(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	dmw := NewDynamicMultiWriter(&buf)
	var wg sync.WaitGroup
	const n = 50
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			_, err := dmw.Write([]byte("a"))
			require.NoError(t, err)
		}()
	}
	wg.Wait()
	require.Equal(t, n, buf.Len())
}
