// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/fatih/color"
	"github.com/stretchr/testify/assert"
)

func init() {
	// Disable color output in tests so assertions don't depend on ANSI codes.
	color.NoColor = true
}

func TestFormatLine_SSEEventSuppressed(t *testing.T) {
	t.Parallel()
	f := newLogFormatter()
	assert.Empty(t, f.formatLine("event: log"))
}

func TestFormatLine_BlankLineSuppressed(t *testing.T) {
	t.Parallel()
	f := newLogFormatter()
	assert.Empty(t, f.formatLine(""))
	assert.Empty(t, f.formatLine("   "))
}

func TestFormatLine_NonSSEPassthrough(t *testing.T) {
	t.Parallel()
	f := newLogFormatter()
	assert.Equal(t, "some random line", f.formatLine("some random line"))
}

func TestFormatLine_StreamEntry(t *testing.T) {
	t.Parallel()
	f := newLogFormatter()

	_ = f.formatLine("event: log")
	result := f.formatLine(`data: {"timestamp":"2026-03-19T12:50:25.788146040+00:00","stream":"stderr","message":"Traceback (most recent call last):"}`)

	assert.Contains(t, result, "stderr")
	assert.Contains(t, result, "Traceback (most recent call last):")
	// Should contain a formatted timestamp, not the raw ISO string
	assert.NotContains(t, result, "2026-03-19T")
}

func TestFormatLine_StreamEntry_Stdout(t *testing.T) {
	t.Parallel()
	f := newLogFormatter()

	_ = f.formatLine("event: log")
	result := f.formatLine(`data: {"timestamp":"2026-03-19T12:50:25.000Z","stream":"stdout","message":"Hello world"}`)

	assert.Contains(t, result, "stdout")
	assert.Contains(t, result, "Hello world")
}

func TestFormatLine_StreamEntry_Status(t *testing.T) {
	t.Parallel()
	f := newLogFormatter()

	_ = f.formatLine("event: log")
	result := f.formatLine(`data: {"timestamp":"2026-03-19T12:50:51.705577528Z","stream":"status","message":"Connecting to the container..."}`)

	assert.Contains(t, result, "status")
	assert.Contains(t, result, "Connecting to the container...")
}

func TestFormatLine_SessionEntry(t *testing.T) {
	t.Parallel()
	f := newLogFormatter()

	_ = f.formatLine("event: log")
	result := f.formatLine(`data: {"timestamp":"2026-03-19T12:50:51.0988803+00:00","session_id":"8f606b6b-5312-4272-958a-dd906de5f5a5","session_state":"Running","agent":"echo-agent","version":"4","last_accessed":"2026-03-19T12:50:25.007+00:00"}`)

	assert.Contains(t, result, "session")
	assert.Contains(t, result, "Running")
	assert.Contains(t, result, "v4")
	assert.Contains(t, result, "last accessed:")
}

func TestFormatLine_MalformedJSON_Fallback(t *testing.T) {
	t.Parallel()
	f := newLogFormatter()

	_ = f.formatLine("event: log")
	result := f.formatLine("data: {not valid json}")

	// Should fall back to returning the raw data (without "data: " prefix)
	assert.Equal(t, "{not valid json}", result)
}

func TestFormatLine_DataWithoutEvent(t *testing.T) {
	t.Parallel()
	f := newLogFormatter()

	// data line without a preceding event line should still be parsed
	result := f.formatLine(`data: {"timestamp":"2026-03-19T12:50:25.000Z","stream":"stdout","message":"orphan line"}`)
	assert.Contains(t, result, "orphan line")
}

func TestFormatLine_TrailingNewlineStripped(t *testing.T) {
	t.Parallel()
	f := newLogFormatter()

	_ = f.formatLine("event: log")
	result := f.formatLine(`data: {"timestamp":"2026-03-19T12:50:25.000Z","stream":"stderr","message":"error message\n"}`)

	// Trailing newlines from message should be stripped
	assert.NotContains(t, result, "\n")
}

func TestFormatLine_EventStateResets(t *testing.T) {
	t.Parallel()
	f := newLogFormatter()

	// First event+data pair
	_ = f.formatLine("event: log")
	r1 := f.formatLine(`data: {"timestamp":"2026-03-19T12:50:25.000Z","stream":"stdout","message":"first"}`)
	assert.Contains(t, r1, "first")

	// Second event+data pair
	_ = f.formatLine("event: log")
	r2 := f.formatLine(`data: {"timestamp":"2026-03-19T12:50:26.000Z","stream":"stderr","message":"second"}`)
	assert.Contains(t, r2, "second")
}

func TestFormatTimestamp_RFC3339Nano(t *testing.T) {
	t.Parallel()
	result := formatTimestamp("2026-03-19T12:50:25.788146040+00:00")
	// Should be HH:MM:SS in local time
	assert.Regexp(t, `^\d{2}:\d{2}:\d{2}$`, result)
}

func TestFormatTimestamp_RFC3339(t *testing.T) {
	t.Parallel()
	result := formatTimestamp("2026-03-19T12:50:25+00:00")
	assert.Regexp(t, `^\d{2}:\d{2}:\d{2}$`, result)
}

func TestFormatTimestamp_ZSuffix(t *testing.T) {
	t.Parallel()
	result := formatTimestamp("2026-03-19T12:50:51.705577528Z")
	assert.Regexp(t, `^\d{2}:\d{2}:\d{2}$`, result)
}

func TestFormatTimestamp_Unparseable(t *testing.T) {
	t.Parallel()
	result := formatTimestamp("not-a-timestamp")
	assert.Equal(t, "not-a-timestamp", result)
}

func TestFormatTimestamp_Empty(t *testing.T) {
	t.Parallel()
	result := formatTimestamp("")
	assert.Equal(t, "", result)
}

func TestFormatLine_FullSSESequence(t *testing.T) {
	t.Parallel()
	f := newLogFormatter()

	// Simulate a full SSE sequence as seen in the real output
	lines := []string{
		"event: log",
		`data: {"timestamp":"2026-03-19T12:50:51.0988803+00:00","session_id":"8f606b6b","session_state":"Running","agent":"echo-agent","version":"4","last_accessed":"2026-03-19T12:50:25.007+00:00"}`,
		"",
		"event: log",
		`data: {"timestamp":"2026-03-19T12:50:51.705577528Z","stream":"status","message":"Connecting to the container..."}`,
		"",
		"event: log",
		`data: {"timestamp":"2026-03-19T12:50:25.788146040+00:00","stream":"stderr","message":"Traceback (most recent call last):"}`,
		"",
		"event: log",
		`data: {"timestamp":"2026-03-19T12:50:51.706107016Z","stream":"status","message":"Successfully connected to container"}`,
	}

	var outputs []string
	for _, line := range lines {
		if out := f.formatLine(line); out != "" {
			outputs = append(outputs, out)
		}
	}

	assert.Len(t, outputs, 4)
	assert.Contains(t, outputs[0], "session")
	assert.Contains(t, outputs[0], "Running")
	assert.Contains(t, outputs[1], "Connecting to the container...")
	assert.Contains(t, outputs[2], "Traceback")
	assert.Contains(t, outputs[3], "Successfully connected")
}
