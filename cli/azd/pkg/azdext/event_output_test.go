// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestEventOutput_DefaultsToStdout(t *testing.T) {
	var stdout bytes.Buffer
	previousFallback := eventOutputFallback
	eventOutputFallback = &stdout
	t.Cleanup(func() {
		eventOutputFallback = previousFallback
	})

	require.Same(t, &stdout, EventOutput(t.Context()))
}

func TestEventOutputWriter_ForwardsOutput(t *testing.T) {
	var stdout bytes.Buffer
	var progress []string
	writer := &eventOutputWriter{
		writer: &stdout,
		progress: func(message string) {
			progress = append(progress, message)
		},
	}

	_, err := writer.Write([]byte("warning\n"))
	require.NoError(t, err)
	require.Equal(t, "warning\n", stdout.String())
	require.Equal(t, []string{"warning\n"}, progress)
}

func TestEventOutputWriter_IsSafeWithoutProgress(t *testing.T) {
	var stdout bytes.Buffer
	writer := &eventOutputWriter{writer: &stdout}

	_, err := writer.Write([]byte("status\n"))
	require.NoError(t, err)
	require.Equal(t, "status\n", stdout.String())
}

func TestEventOutputWriter_SplitsLargeProgressWrites(t *testing.T) {
	data := bytes.Repeat([]byte("x"), maxProgressMessageBytes*2+1)
	var progress []string
	writer := &eventOutputWriter{
		writer: io.Discard,
		progress: func(message string) {
			progress = append(progress, message)
		},
	}

	written, err := writer.Write(data)
	require.NoError(t, err)
	require.Equal(t, len(data), written)
	require.Len(t, progress, 3)
	require.Equal(t, string(data), strings.Join(progress, ""))
	for _, message := range progress {
		require.LessOrEqual(t, len(message), maxProgressMessageBytes)
	}
}

func TestEventOutputWriter_SplitsProgressAtUTF8Boundaries(t *testing.T) {
	data := []byte(strings.Repeat("a", maxProgressMessageBytes-1) + "\u20ac")
	var progress []string
	writer := &eventOutputWriter{
		writer: io.Discard,
		progress: func(message string) {
			progress = append(progress, message)
		},
	}

	written, err := writer.Write(data)
	require.NoError(t, err)
	require.Equal(t, len(data), written)
	require.Len(t, progress, 2)
	for _, message := range progress {
		require.True(t, utf8.ValidString(message))
		require.LessOrEqual(t, len(message), maxProgressMessageBytes)
	}
	require.Equal(t, string(data), strings.Join(progress, ""))
}

func TestEventOutputWriter_ReplacesInvalidProgressUTF8(t *testing.T) {
	data := append(bytes.Repeat([]byte("a"), maxProgressMessageBytes-1), 0xff)
	var progress []string
	writer := &eventOutputWriter{
		writer: io.Discard,
		progress: func(message string) {
			progress = append(progress, message)
		},
	}

	written, err := writer.Write(data)
	require.NoError(t, err)
	require.Equal(t, len(data), written)
	progressOutput := strings.Join(progress, "")
	require.True(t, utf8.ValidString(progressOutput))
	require.Equal(t, strings.Repeat("a", maxProgressMessageBytes-1)+"\uFFFD", progressOutput)
}
