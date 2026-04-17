// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/fatih/color"
)

// logFormatter renders SSE log events from the agent logstream endpoint into a
// compact, human-readable form. See issue #7209 for the design.
//
// The logstream endpoint emits Server-Sent Events (SSE) with two JSON payload
// shapes today:
//
//   - Session metadata:
//     {"timestamp":"...","session_id":"...","session_state":"Running",
//     "agent":"...","version":"3","last_accessed":"..."}
//
//   - Log event:
//     {"timestamp":"...","stream":"stdout|stderr|status","message":"..."}
//
// Unknown payload shapes or non-JSON data lines are passed through verbatim
// so the command remains useful if the API adds new event types.
type logFormatter struct {
	writer io.Writer
	// utc controls whether timestamps are rendered in UTC or local time.
	utc bool
	// raw disables parsing and prints the stream verbatim. Used by --raw for
	// scripts that rely on the original SSE output.
	raw bool
}

// logPayload covers both session-metadata and log-event shapes. Fields are
// optional; the rendering code switches on which ones are populated.
type logPayload struct {
	Timestamp    string `json:"timestamp"`
	Stream       string `json:"stream"`
	Message      string `json:"message"`
	SessionState string `json:"session_state"`
	Agent        string `json:"agent"`
	Version      string `json:"version"`
	LastAccessed string `json:"last_accessed"`
}

// Column widths for the compact output. Chosen so the widest known stream
// name ("session") fits without truncation.
const (
	timeColWidth   = 8
	streamColWidth = 7
)

// formatStream reads an SSE stream from r and writes formatted log lines to
// f.writer until EOF, ctx cancellation, or a read error. Context cancellation
// and deadline errors are suppressed (they're the normal way to end a
// non-follow fetch or a user Ctrl+C) and return nil.
func (f *logFormatter) formatStream(ctx context.Context, r io.Reader) error {
	scanner := bufio.NewScanner(r)
	// Log lines can include stack traces or large JSON dumps; match the
	// upper bound used by readSSEStream in invoke.go (1 MB per line).
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var (
		eventName string
		dataLines []string
	)

	flush := func() {
		if len(dataLines) == 0 {
			return
		}
		data := strings.Join(dataLines, "\n")
		f.renderEvent(eventName, data)
		eventName = ""
		dataLines = dataLines[:0]
	}

	for scanner.Scan() {
		line := scanner.Text()

		if f.raw {
			fmt.Fprintln(f.writer, line)
			continue
		}

		switch {
		case line == "":
			// Blank line terminates an event.
			flush()
		case strings.HasPrefix(line, ":"):
			// SSE comment line; ignore.
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			// Trim a single leading space per SSE spec, but preserve the rest.
			payload := strings.TrimPrefix(line, "data:")
			payload = strings.TrimPrefix(payload, " ")
			dataLines = append(dataLines, payload)
		default:
			// id:, retry:, or an unrecognized line — pass through so nothing
			// is silently dropped.
			fmt.Fprintln(f.writer, line)
		}
	}
	// Flush a trailing event that wasn't followed by a blank line (EOF case).
	flush()

	if err := scanner.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil
		}
		return fmt.Errorf("error reading log stream: %w", err)
	}
	// Also suppress a ctx error that fires at the reader level without
	// surfacing as scanner.Err().
	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.DeadlineExceeded) || errors.Is(ctxErr, context.Canceled) {
			return nil
		}
	}
	return nil
}

// renderEvent parses a single SSE event payload and writes the formatted
// line(s) to f.writer. Non-"log" events and payloads that cannot be parsed
// are written raw so the command remains forward-compatible.
func (f *logFormatter) renderEvent(eventName, data string) {
	// Accept events with no "event:" line (data-only) and "event: log".
	// Anything else we don't recognize is passed through raw.
	if eventName != "" && eventName != "log" {
		fmt.Fprintf(f.writer, "event: %s\ndata: %s\n\n", eventName, data)
		return
	}

	var payload logPayload
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		fmt.Fprintln(f.writer, data)
		return
	}

	ts := f.formatTimestamp(payload.Timestamp)

	// Session-metadata events have session_state but no stream.
	if payload.SessionState != "" && payload.Stream == "" {
		f.renderSessionEvent(ts, payload)
		return
	}

	// Log events must have a stream; if not, fall back to raw so nothing is
	// silently dropped.
	if payload.Stream == "" {
		fmt.Fprintln(f.writer, data)
		return
	}

	f.renderLogEvent(ts, payload.Stream, payload.Message)
}

// renderSessionEvent writes a session-metadata event as:
//
//	HH:MM:SS  session <State>  (v<version>, last accessed: HH:MM:SS)
func (f *logFormatter) renderSessionEvent(ts string, payload logPayload) {
	streamLabel := padStream("session")
	sessionColor := color.New(color.FgGreen).SprintFunc()

	var suffix strings.Builder
	if payload.Version != "" || payload.LastAccessed != "" {
		suffix.WriteString(" (")
		wrote := false
		if payload.Version != "" {
			fmt.Fprintf(&suffix, "v%s", payload.Version)
			wrote = true
		}
		if payload.LastAccessed != "" {
			if wrote {
				suffix.WriteString(", ")
			}
			fmt.Fprintf(&suffix, "last accessed: %s", f.formatTimestamp(payload.LastAccessed))
		}
		suffix.WriteString(")")
	}

	fmt.Fprintf(
		f.writer, "%s  %s %s%s\n",
		ts, sessionColor(streamLabel), payload.SessionState, suffix.String(),
	)
}

// renderLogEvent writes a single log event as:
//
//	HH:MM:SS  <stream>  <message first line>
//	                   <continuation lines re-indented>
//
// Embedded newlines in message (common for stack traces) are indented so the
// time/stream columns line up.
func (f *logFormatter) renderLogEvent(ts, stream, message string) {
	streamLabel := padStream(stream)
	coloredLabel := colorizeStream(stream, streamLabel)

	// Continuation lines are indented to align under the message column.
	// Indent = time column width + 2 spaces + stream column width + 2 spaces.
	indent := strings.Repeat(" ", timeColWidth+2+streamColWidth+2)

	lines := strings.Split(message, "\n")
	for i, ln := range lines {
		if i == 0 {
			fmt.Fprintf(f.writer, "%s  %s  %s\n", ts, coloredLabel, ln)
		} else {
			fmt.Fprintf(f.writer, "%s%s\n", indent, ln)
		}
	}
}

// colorizeStream applies the per-stream color. If color is disabled (via the
// NO_COLOR env var or terminal detection), the label is returned unchanged.
func colorizeStream(stream, label string) string {
	var c *color.Color
	switch stream {
	case "stderr":
		c = color.New(color.FgRed)
	case "status":
		c = color.New(color.FgCyan)
	case "stdout":
		// default color; no wrapping
		return label
	default:
		return label
	}
	return c.SprintFunc()(label)
}

// padStream right-pads a stream label to streamColWidth. Labels longer than
// the column are returned without padding to avoid truncation.
func padStream(s string) string {
	if len(s) >= streamColWidth {
		return s
	}
	return s + strings.Repeat(" ", streamColWidth-len(s))
}

// formatTimestamp parses the server timestamp (RFC3339 with optional
// nanoseconds) and renders it as HH:MM:SS in local or UTC time. If parsing
// fails, the original string is returned so information is not lost.
func (f *logFormatter) formatTimestamp(raw string) string {
	if raw == "" {
		return strings.Repeat(" ", timeColWidth)
	}
	// time.RFC3339Nano handles both `Z` and `+00:00` offsets and nanosecond
	// precision. Some payloads use microsecond precision which also parses
	// correctly with this layout.
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		if t2, err2 := time.Parse(time.RFC3339, raw); err2 == nil {
			t = t2
		} else {
			return raw
		}
	}
	if !f.utc {
		t = t.Local()
	} else {
		t = t.UTC()
	}
	return t.Format("15:04:05")
}
