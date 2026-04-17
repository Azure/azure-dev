// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/fatih/color"
	"github.com/stretchr/testify/require"
)

func init() {
	// Disable color globally for stable assertions in this test file.
	color.NoColor = true
}

func newTestFormatter(utc, raw bool) (*logFormatter, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return &logFormatter{writer: buf, utc: utc, raw: raw}, buf
}

// formatStream reads one SSE event and emits a formatted line.
func TestFormatStream_LogEvent_Stdout(t *testing.T) {
	f, buf := newTestFormatter(true, false)

	input := "event: log\n" +
		"data: {\"timestamp\":\"2026-03-19T16:40:03.399273237+00:00\"," +
		"\"stream\":\"stdout\",\"message\":\"Seattle Hotel Agent Server running on http://localhost:8088\"}\n" +
		"\n"

	err := f.formatStream(t.Context(), strings.NewReader(input))
	require.NoError(t, err)
	out := buf.String()
	require.Contains(t, out, "16:40:03")
	require.Contains(t, out, "stdout")
	require.Contains(t, out, "Seattle Hotel Agent Server running on http://localhost:8088")
}

func TestFormatStream_LogEvent_Stderr(t *testing.T) {
	f, buf := newTestFormatter(true, false)

	input := "event: log\n" +
		"data: {\"timestamp\":\"2026-03-19T16:40:03.420Z\"," +
		"\"stream\":\"stderr\",\"message\":\"INFO:     Started server process [6]\"}\n" +
		"\n"

	err := f.formatStream(t.Context(), strings.NewReader(input))
	require.NoError(t, err)
	out := buf.String()
	require.Contains(t, out, "16:40:03")
	require.Contains(t, out, "stderr")
	require.Contains(t, out, "Started server process [6]")
}

func TestFormatStream_SessionMetadata(t *testing.T) {
	f, buf := newTestFormatter(true, false)

	input := "event: log\n" +
		"data: {\"timestamp\":\"2026-03-19T16:45:26.74179+00:00\"," +
		"\"session_id\":\"abc-123\",\"session_state\":\"Running\"," +
		"\"agent\":\"seattle-hotel-agent\",\"version\":\"3\"," +
		"\"last_accessed\":\"2026-03-19T16:40:01.351+00:00\"}\n" +
		"\n"

	err := f.formatStream(t.Context(), strings.NewReader(input))
	require.NoError(t, err)
	out := buf.String()
	require.Contains(t, out, "16:45:26")
	require.Contains(t, out, "session")
	require.Contains(t, out, "Running")
	require.Contains(t, out, "v3")
	require.Contains(t, out, "last accessed: 16:40:01")
}

// Session metadata rendering degrades gracefully when optional fields are missing.
func TestFormatStream_SessionMetadata_MissingOptionalFields(t *testing.T) {
	f, buf := newTestFormatter(true, false)

	input := "event: log\n" +
		"data: {\"timestamp\":\"2026-03-19T16:45:26Z\"," +
		"\"session_state\":\"Running\"}\n" +
		"\n"

	err := f.formatStream(t.Context(), strings.NewReader(input))
	require.NoError(t, err)
	out := buf.String()
	require.Contains(t, out, "session")
	require.Contains(t, out, "Running")
	require.NotContains(t, out, "last accessed:")
	require.NotContains(t, out, "(v")
}

func TestFormatStream_StatusEvent(t *testing.T) {
	f, buf := newTestFormatter(true, false)

	input := "event: log\n" +
		"data: {\"timestamp\":\"2026-03-19T16:45:27.299Z\"," +
		"\"stream\":\"status\",\"message\":\"Connecting to the container...\"}\n" +
		"\n"

	err := f.formatStream(t.Context(), strings.NewReader(input))
	require.NoError(t, err)
	out := buf.String()
	require.Contains(t, out, "status")
	require.Contains(t, out, "Connecting to the container...")
}

// Multiple events in a single stream are each rendered on their own line.
func TestFormatStream_MultipleEvents(t *testing.T) {
	f, buf := newTestFormatter(true, false)

	input := "event: log\n" +
		"data: {\"timestamp\":\"2026-03-19T16:40:03Z\",\"stream\":\"stdout\",\"message\":\"first\"}\n" +
		"\n" +
		"event: log\n" +
		"data: {\"timestamp\":\"2026-03-19T16:40:04Z\",\"stream\":\"stderr\",\"message\":\"second\"}\n" +
		"\n"

	err := f.formatStream(t.Context(), strings.NewReader(input))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 2)
	require.Contains(t, lines[0], "stdout")
	require.Contains(t, lines[0], "first")
	require.Contains(t, lines[1], "stderr")
	require.Contains(t, lines[1], "second")
}

// Multi-line data: fields are concatenated per SSE spec.
func TestFormatStream_MultilineDataField(t *testing.T) {
	f, buf := newTestFormatter(true, false)

	// Two data: lines whose concatenation forms valid JSON.
	input := "event: log\n" +
		"data: {\"timestamp\":\"2026-03-19T16:40:03Z\",\"stream\":\"stdout\",\n" +
		"data: \"message\":\"joined\"}\n" +
		"\n"

	err := f.formatStream(t.Context(), strings.NewReader(input))
	require.NoError(t, err)
	require.Contains(t, buf.String(), "joined")
}

// An event without the trailing blank line is still flushed at EOF.
func TestFormatStream_FlushOnEOF(t *testing.T) {
	f, buf := newTestFormatter(true, false)

	input := "event: log\n" +
		"data: {\"timestamp\":\"2026-03-19T16:40:03Z\",\"stream\":\"stdout\",\"message\":\"no blank\"}"

	err := f.formatStream(t.Context(), strings.NewReader(input))
	require.NoError(t, err)
	require.Contains(t, buf.String(), "no blank")
}

// Malformed JSON falls back to printing the raw data line.
func TestFormatStream_MalformedJsonRawFallback(t *testing.T) {
	f, buf := newTestFormatter(true, false)

	input := "event: log\n" +
		"data: {not json}\n" +
		"\n"

	err := f.formatStream(t.Context(), strings.NewReader(input))
	require.NoError(t, err)
	require.Contains(t, buf.String(), "{not json}")
}

// Unknown event names are passed through as raw SSE to avoid silent drops.
func TestFormatStream_UnknownEventName(t *testing.T) {
	f, buf := newTestFormatter(true, false)

	input := "event: error\n" +
		"data: {\"code\":\"x\"}\n" +
		"\n"

	err := f.formatStream(t.Context(), strings.NewReader(input))
	require.NoError(t, err)
	out := buf.String()
	require.Contains(t, out, "event: error")
	require.Contains(t, out, "data: {\"code\":\"x\"}")
}

// Event with no "event:" line (data-only) is still formatted.
func TestFormatStream_DataOnlyEvent(t *testing.T) {
	f, buf := newTestFormatter(true, false)

	input := "data: {\"timestamp\":\"2026-03-19T16:40:03Z\"," +
		"\"stream\":\"stdout\",\"message\":\"orphan\"}\n" +
		"\n"

	err := f.formatStream(t.Context(), strings.NewReader(input))
	require.NoError(t, err)
	require.Contains(t, buf.String(), "orphan")
}

// SSE comment lines (starting with ":") are ignored without breaking the
// current event.
func TestFormatStream_CommentLinesIgnored(t *testing.T) {
	f, buf := newTestFormatter(true, false)

	input := ": heartbeat\n" +
		"event: log\n" +
		": another comment\n" +
		"data: {\"timestamp\":\"2026-03-19T16:40:03Z\",\"stream\":\"stdout\",\"message\":\"kept\"}\n" +
		"\n"

	err := f.formatStream(t.Context(), strings.NewReader(input))
	require.NoError(t, err)
	require.Contains(t, buf.String(), "kept")
	require.NotContains(t, buf.String(), "heartbeat")
}

// A message containing embedded newlines (e.g., a stack trace) is split and
// continuation lines are indented to align under the message column.
func TestFormatStream_MessageWithEmbeddedNewlines(t *testing.T) {
	f, buf := newTestFormatter(true, false)

	input := "event: log\n" +
		"data: {\"timestamp\":\"2026-03-19T16:40:03Z\"," +
		"\"stream\":\"stderr\",\"message\":\"Traceback\\n  File x.py\\n    raise Exception\"}\n" +
		"\n"

	err := f.formatStream(t.Context(), strings.NewReader(input))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 3)
	require.Contains(t, lines[0], "16:40:03")
	require.Contains(t, lines[0], "stderr")
	require.Contains(t, lines[0], "Traceback")
	// Continuation lines should begin with whitespace only (no timestamp).
	require.True(t, strings.HasPrefix(lines[1], "   "))
	require.Contains(t, lines[1], "File x.py")
	require.True(t, strings.HasPrefix(lines[2], "   "))
	require.Contains(t, lines[2], "raise Exception")
}

// An event with no stream and no session_state falls through to raw output.
func TestFormatStream_UnknownPayloadShape(t *testing.T) {
	f, buf := newTestFormatter(true, false)

	input := "event: log\n" +
		"data: {\"timestamp\":\"2026-03-19T16:40:03Z\",\"foo\":\"bar\"}\n" +
		"\n"

	err := f.formatStream(t.Context(), strings.NewReader(input))
	require.NoError(t, err)
	require.Contains(t, buf.String(), "\"foo\":\"bar\"")
}

// Raw mode prints the stream verbatim without any parsing.
func TestFormatStream_RawMode(t *testing.T) {
	f, buf := newTestFormatter(true, true)

	input := "event: log\n" +
		"data: {\"timestamp\":\"2026-03-19T16:40:03Z\",\"stream\":\"stdout\",\"message\":\"raw\"}\n" +
		"\n"

	err := f.formatStream(t.Context(), strings.NewReader(input))
	require.NoError(t, err)
	out := buf.String()
	require.Contains(t, out, "event: log")
	require.Contains(t, out, "\"message\":\"raw\"")
}

// Invalid timestamps are preserved verbatim so information is not lost.
func TestFormatTimestamp_InvalidPreserved(t *testing.T) {
	f := &logFormatter{utc: true}
	require.Equal(t, "not-a-time", f.formatTimestamp("not-a-time"))
}

func TestFormatTimestamp_EmptyPadded(t *testing.T) {
	f := &logFormatter{utc: true}
	require.Equal(t, strings.Repeat(" ", timeColWidth), f.formatTimestamp(""))
}

func TestFormatTimestamp_UtcVsLocal(t *testing.T) {
	input := "2026-03-19T16:40:03.420Z"

	utc := &logFormatter{utc: true}
	require.Equal(t, "16:40:03", utc.formatTimestamp(input))

	// Local time format depends on TZ, but must still be a HH:MM:SS form.
	local := &logFormatter{utc: false}
	got := local.formatTimestamp(input)
	require.Len(t, got, 8)
	require.Equal(t, ":", string(got[2]))
	require.Equal(t, ":", string(got[5]))
}

// Cancellation is suppressed: the formatter returns nil when the reader ends
// with a canceled context.
func TestFormatStream_ContextCanceledSuppressed(t *testing.T) {
	f, _ := newTestFormatter(true, false)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := f.formatStream(ctx, strings.NewReader(""))
	require.NoError(t, err)
}

func TestPadStream(t *testing.T) {
	require.Equal(t, "stdout ", padStream("stdout"))
	require.Equal(t, "stderr ", padStream("stderr"))
	require.Equal(t, "session", padStream("session"))
	// Oversized labels are returned as-is.
	require.Equal(t, "very-long-name", padStream("very-long-name"))
}
