// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/fatih/color"
)

// OutputFormat represents the output format for extension commands.
type OutputFormat string

const (
	// OutputFormatDefault is human-readable text with optional color.
	OutputFormatDefault OutputFormat = "default"
	// OutputFormatJSON outputs structured JSON for machine consumption.
	OutputFormatJSON OutputFormat = "json"
)

// ParseOutputFormat converts a string to an OutputFormat.
// Returns OutputFormatDefault for unrecognized values and a non-nil error.
func ParseOutputFormat(s string) (OutputFormat, error) {
	switch strings.ToLower(s) {
	case "default", "":
		return OutputFormatDefault, nil
	case "json":
		return OutputFormatJSON, nil
	default:
		return OutputFormatDefault, fmt.Errorf("invalid output format %q (valid: default, json)", s)
	}
}

// OutputOptions configures an [Output] instance.
type OutputOptions struct {
	// Format controls the output style. Defaults to OutputFormatDefault.
	Format OutputFormat
	// Writer is the destination for normal output. Defaults to os.Stdout.
	Writer io.Writer
	// ErrWriter is the destination for error/warning output. Defaults to os.Stderr.
	ErrWriter io.Writer
}

// Output provides formatted, format-aware output for extension commands.
// In default mode it writes human-readable text with ANSI color; in JSON mode
// it writes structured JSON objects to stdout and suppresses decorative output.
//
// Output is safe for use from a single goroutine. If concurrent use is needed
// callers should synchronize externally.
type Output struct {
	writer    io.Writer
	errWriter io.Writer
	format    OutputFormat

	// Color printers — configured once at construction.
	successColor *color.Color
	warningColor *color.Color
	errorColor   *color.Color
	infoColor    *color.Color
	headerColor  *color.Color
	dimColor     *color.Color
}

// NewOutput creates an Output configured by opts.
// If opts.Writer or opts.ErrWriter are nil they default to os.Stdout / os.Stderr.
func NewOutput(opts OutputOptions) *Output {
	w := opts.Writer
	if w == nil {
		w = os.Stdout
	}
	ew := opts.ErrWriter
	if ew == nil {
		ew = os.Stderr
	}

	return &Output{
		writer:       w,
		errWriter:    ew,
		format:       opts.Format,
		successColor: color.New(color.FgGreen),
		warningColor: color.New(color.FgYellow),
		errorColor:   color.New(color.FgRed),
		infoColor:    color.New(color.FgCyan),
		headerColor:  color.New(color.Bold),
		dimColor:     color.New(color.Faint),
	}
}

// IsJSON returns true when the output format is JSON.
// Callers can use this to skip decorative output that is only relevant in
// human-readable mode.
func (o *Output) IsJSON() bool {
	return o.format == OutputFormatJSON
}

// Success prints a success message prefixed with a green check mark.
// In JSON mode the call is a no-op (use [Output.JSON] for structured data).
func (o *Output) Success(format string, args ...any) {
	if o.IsJSON() {
		return
	}
	msg := fmt.Sprintf(format, args...)
	o.successColor.Fprintf(o.writer, "(✓) Done: %s\n", msg)
}

// Warning prints a warning message prefixed with a yellow exclamation mark.
// Warnings are written to ErrWriter in both default and JSON mode so they are
// visible even when stdout is piped through a JSON consumer.
func (o *Output) Warning(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if o.IsJSON() {
		// In JSON mode emit a structured warning to stderr.
		_ = json.NewEncoder(o.errWriter).Encode(map[string]string{
			"level":   "warning",
			"message": msg,
		})
		return
	}
	o.warningColor.Fprintf(o.errWriter, "(!) Warning: %s\n", msg)
}

// Error prints an error message prefixed with a red cross.
// Errors are always written to ErrWriter.
func (o *Output) Error(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if o.IsJSON() {
		_ = json.NewEncoder(o.errWriter).Encode(map[string]string{
			"level":   "error",
			"message": msg,
		})
		return
	}
	o.errorColor.Fprintf(o.errWriter, "(✗) Error: %s\n", msg)
}

// Info prints an informational message prefixed with an info symbol.
// In JSON mode the call is a no-op (use [Output.JSON] for structured data).
func (o *Output) Info(format string, args ...any) {
	if o.IsJSON() {
		return
	}
	msg := fmt.Sprintf(format, args...)
	o.infoColor.Fprintf(o.writer, "(i) %s\n", msg)
}

// Message prints an undecorated message to stdout.
// In JSON mode the call is a no-op.
func (o *Output) Message(format string, args ...any) {
	if o.IsJSON() {
		return
	}
	fmt.Fprintf(o.writer, format+"\n", args...)
}

// JSON writes data as a pretty-printed JSON object to stdout.
// It is active in all output modes so callers can unconditionally emit
// structured payloads (in default mode the JSON is still human-readable).
func (o *Output) JSON(data any) error {
	enc := json.NewEncoder(o.writer)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		return fmt.Errorf("output: failed to encode JSON: %w", err)
	}
	return nil
}

// Table prints a formatted text table with headers and rows.
// In JSON mode the table is emitted as a JSON array of objects instead.
//
// headers defines the column names. Each row is a slice of cell values
// with the same length as headers. Rows with fewer cells are padded with
// empty strings; extra cells are silently ignored.
func (o *Output) Table(headers []string, rows [][]string) {
	if len(headers) == 0 {
		return
	}

	if o.IsJSON() {
		o.tableJSON(headers, rows)
		return
	}

	o.tableText(headers, rows)
}

// tableJSON emits the table as a JSON array of objects keyed by header name.
func (o *Output) tableJSON(headers []string, rows [][]string) {
	out := make([]map[string]string, 0, len(rows))
	for _, row := range rows {
		obj := make(map[string]string, len(headers))
		for i, h := range headers {
			if i < len(row) {
				obj[h] = row[i]
			} else {
				obj[h] = ""
			}
		}
		out = append(out, obj)
	}
	_ = o.JSON(out)
}

// tableText renders an aligned text table with a header separator.
func (o *Output) tableText(headers []string, rows [][]string) {
	// Calculate column widths.
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i := range headers {
			if i < len(row) && len(row[i]) > widths[i] {
				widths[i] = len(row[i])
			}
		}
	}

	// Print header row.
	for i, h := range headers {
		if i > 0 {
			fmt.Fprint(o.writer, "  ")
		}
		o.headerColor.Fprintf(o.writer, "%-*s", widths[i], h)
	}
	fmt.Fprintln(o.writer)

	// Print separator.
	for i, w := range widths {
		if i > 0 {
			fmt.Fprint(o.writer, "  ")
		}
		fmt.Fprint(o.writer, strings.Repeat("─", w))
	}
	fmt.Fprintln(o.writer)

	// Print data rows.
	for _, row := range rows {
		for i := range headers {
			if i > 0 {
				fmt.Fprint(o.writer, "  ")
			}
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			fmt.Fprintf(o.writer, "%-*s", widths[i], cell)
		}
		fmt.Fprintln(o.writer)
	}
}
