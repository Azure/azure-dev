// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/fatih/color"
)

// logStreamEntry represents a container log line (stdout, stderr, or status message).
type logStreamEntry struct {
	Timestamp string `json:"timestamp"`
	Stream    string `json:"stream"`
	Message   string `json:"message"`
}

// logSessionEntry represents a session-level status event.
type logSessionEntry struct {
	Timestamp    string `json:"timestamp"`
	SessionID    string `json:"session_id"`
	SessionState string `json:"session_state"`
	Agent        string `json:"agent"`
	Version      string `json:"version"`
	LastAccessed string `json:"last_accessed"`
}

// logFormatter parses SSE log lines from the monitor stream and produces
// human-readable, color-coded output.
type logFormatter struct {
	pendingEvent string // last seen "event: <type>" value

	// Color printers, auto-disabled when NO_COLOR is set or stdout is not a TTY.
	dimColor    *color.Color
	stderrColor *color.Color
	statusColor *color.Color
	sessionColor *color.Color
}

func newLogFormatter() *logFormatter {
	return &logFormatter{
		dimColor:     color.New(color.Faint),
		stderrColor:  color.New(color.FgRed),
		statusColor:  color.New(color.FgCyan),
		sessionColor: color.New(color.FgGreen),
	}
}

// formatLine processes a single raw line from the SSE stream.
// Returns the formatted string to print, or empty string if the line
// should be suppressed (e.g. an "event:" prefix line).
func (f *logFormatter) formatLine(line string) string {
	// SSE event prefix — remember it for the next data line.
	if after, ok := strings.CutPrefix(line, "event: "); ok {
		f.pendingEvent = strings.TrimSpace(after)
		return ""
	}

	// SSE data payload — parse and format.
	if data, ok := strings.CutPrefix(line, "data: "); ok {
		defer func() { f.pendingEvent = "" }()
		return f.formatData(data)
	}

	// Blank lines are SSE event separators — suppress.
	if strings.TrimSpace(line) == "" {
		return ""
	}

	// Anything else (unexpected format) — pass through as-is.
	return line
}

// formatData parses the JSON payload from a "data: " line and returns
// a formatted string. Falls back to the raw data on parse failure.
func (f *logFormatter) formatData(data string) string {
	// Try stream entry first (most common).
	var stream logStreamEntry
	if err := json.Unmarshal([]byte(data), &stream); err == nil && stream.Stream != "" {
		return f.formatStreamEntry(&stream)
	}

	// Try session status entry.
	var session logSessionEntry
	if err := json.Unmarshal([]byte(data), &session); err == nil && session.SessionState != "" {
		return f.formatSessionEntry(&session)
	}

	// Unrecognized JSON or plain text — return as-is.
	return data
}

// formatStreamEntry formats a container log line: "HH:MM:SS  stream  message"
func (f *logFormatter) formatStreamEntry(entry *logStreamEntry) string {
	ts := formatTimestamp(entry.Timestamp)
	msg := strings.TrimRight(entry.Message, "\n")
	label := entry.Stream

	var labelStr string
	switch label {
	case "stderr":
		labelStr = f.stderrColor.Sprint(label)
	case "status":
		labelStr = f.statusColor.Sprint(label)
	default:
		labelStr = label
	}

	return fmt.Sprintf("%s  %-8s %s",
		f.dimColor.Sprint(ts),
		labelStr,
		msg,
	)
}

// formatSessionEntry formats a session status line with state, version, and last accessed time.
func (f *logFormatter) formatSessionEntry(entry *logSessionEntry) string {
	ts := formatTimestamp(entry.Timestamp)
	lastAccessed := formatTimestamp(entry.LastAccessed)

	stateStr := f.sessionColor.Sprint(entry.SessionState)
	details := fmt.Sprintf("v%s, last accessed: %s", entry.Version, lastAccessed)

	return fmt.Sprintf("%s  %-8s %s  (%s)",
		f.dimColor.Sprint(ts),
		f.sessionColor.Sprint("session"),
		stateStr,
		details,
	)
}

// formatTimestamp parses an ISO 8601 timestamp and returns a short local-time
// string in "HH:MM:SS" format. Returns the original string on parse failure.
func formatTimestamp(raw string) string {
	// Try common formats produced by the agent service.
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999999Z",
		"2006-01-02T15:04:05.999999999+00:00",
	} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.Local().Format("15:04:05")
		}
	}
	return raw
}
