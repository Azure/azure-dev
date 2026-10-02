// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewJsonEventWriterFromEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	t.Setenv("AZD_OUTPUT_JSONL", path)

	writer := NewJsonEventWriterFromEnv()

	require.True(t, writer.Enabled())
	require.Equal(t, path, writer.path)
}

func TestJsonEventWriter_AppendsCompactJsonLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("{\"existing\":true}\n"), 0o600))

	writer := NewJsonEventWriter(path)
	require.NoError(t, writer.Open())
	require.NoError(t, writer.Write(map[string]any{"message": "first"}))
	require.NoError(t, writer.Write(map[string]any{"message": "second"}))
	require.NoError(t, writer.Close())

	file, err := os.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, file.Close())
	})

	scanner := bufio.NewScanner(file)
	var lines []map[string]any
	for scanner.Scan() {
		var line map[string]any
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &line))
		lines = append(lines, line)
	}
	require.NoError(t, scanner.Err())
	require.Len(t, lines, 3)
	require.Equal(t, true, lines[0]["existing"])
	require.Equal(t, "first", lines[1]["message"])
	require.Equal(t, "second", lines[2]["message"])
}

func TestJsonEventWriter_ConcurrentWritesRemainValidJsonLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	writer := NewJsonEventWriter(path)
	require.NoError(t, writer.Open())

	const eventCount = 50
	var wg sync.WaitGroup
	errs := make(chan error, eventCount)
	for i := range eventCount {
		wg.Go(func() {
			errs <- writer.Write(map[string]int{"index": i})
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())

	file, err := os.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, file.Close())
	})

	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		var line map[string]int
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &line))
		count++
	}
	require.NoError(t, scanner.Err())
	require.Equal(t, eventCount, count)
}

func TestJsonEventWriter_Disabled(t *testing.T) {
	writer := NewJsonEventWriter("")
	require.False(t, writer.Enabled())
	require.NoError(t, writer.Open())
	require.NoError(t, writer.Write(map[string]bool{"ignored": true}))
	require.NoError(t, writer.Close())
}

func TestJsonEventStreamWriter_MirrorsCompleteAndPartialLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	eventWriter := NewJsonEventWriter(path)
	require.NoError(t, eventWriter.Open())

	var terminal bytes.Buffer
	writer := NewJsonEventStreamWriter(&terminal, eventWriter, "stdout")
	_, err := writer.Write([]byte("\x1b[32mfirst"))
	require.NoError(t, err)
	_, err = writer.Write([]byte(" line\x1b[0m\nsecond line"))
	require.NoError(t, err)
	require.NoError(t, eventWriter.FlushStreams())
	require.NoError(t, eventWriter.Close())

	require.Equal(t, "\x1b[32mfirst line\x1b[0m\nsecond line", terminal.String())

	file, err := os.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, file.Close())
	})

	scanner := bufio.NewScanner(file)
	var events []map[string]any
	for scanner.Scan() {
		var event map[string]any
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &event))
		events = append(events, event)
	}
	require.NoError(t, scanner.Err())
	require.Len(t, events, 2)

	firstData, ok := events[0]["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "first line\n", firstData["message"])
	require.Equal(t, "stdout", firstData["stream"])

	secondData, ok := events[1]["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "second line\n", secondData["message"])
	require.Equal(t, "stdout", secondData["stream"])
}
