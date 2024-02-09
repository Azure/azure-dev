package vsrpc

import (
	"bytes"
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
