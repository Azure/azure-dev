// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"errors"
	"fmt"
	"io"
	"text/template"
)

// emptyTemplate renders an empty string for any data row. It backs the
// header-only "..." placeholder column at the compact breakpoint.
var emptyTemplate = template.Must(template.New("empty").Parse(""))

// PrettyTableFormatter renders tabular data with responsive breakpoints.
// It supports three layout modes based on terminal width: full table, compact
// table (fewer columns), and card layout.
type PrettyTableFormatter struct {
	// ConsoleWidthFn returns the current terminal width. Defaults to getConsoleWidth.
	ConsoleWidthFn func() int
}

func (f *PrettyTableFormatter) Kind() Format {
	return TableFormat
}

func (f *PrettyTableFormatter) Format(obj any, writer io.Writer, opts any) error {
	options, ok := opts.(PrettyTableFormatterOptions)
	if !ok {
		return errors.New("invalid formatter options, PrettyTableFormatterOptions expected")
	}

	if len(options.Columns) == 0 {
		return errors.New("no columns were defined, table format is not supported for this command")
	}

	rows, err := convertToSlice(obj)
	if err != nil {
		return err
	}

	parsed, err := parseColumns(options.Columns)
	if err != nil {
		return err
	}

	widthFn := f.ConsoleWidthFn
	if widthFn == nil {
		widthFn = getConsoleWidth
	}
	termWidth := widthFn()

	if options.ForceCards {
		return f.formatGroupedCards(parsed, rows, termWidth, writer, options)
	}

	switch resolveBreakpoint(termWidth, options) {
	case breakpointFull:
		return f.renderFullTable(parsed, rows, termWidth, writer, options)
	case breakpointCompact:
		return f.renderCompactTable(parsed, rows, termWidth, writer, options)
	default:
		return f.formatGroupedCards(parsed, rows, termWidth, writer, options)
	}
}

// resolveBreakpoint maps a terminal width to a responsive layout breakpoint.
func resolveBreakpoint(termWidth int, options PrettyTableFormatterOptions) breakpoint {
	fullT := options.FullThreshold
	if fullT <= 0 {
		fullT = DefaultFullThreshold
	}
	compactT := options.CompactThreshold
	if compactT <= 0 {
		compactT = DefaultCompactThreshold
	}

	switch {
	case termWidth >= fullT:
		return breakpointFull
	case termWidth >= compactT:
		return breakpointCompact
	default:
		return breakpointCard
	}
}

// parseColumns compiles the value templates for each column and validates that
// headings are unique.
func parseColumns(columns []PrettyColumn) ([]resolvedColumn, error) {
	parsed := make([]resolvedColumn, len(columns))
	seenHeadings := make(map[string]bool, len(columns))
	for i, c := range columns {
		if seenHeadings[c.Heading] {
			return nil, fmt.Errorf("duplicate column heading %q", c.Heading)
		}
		seenHeadings[c.Heading] = true

		t, err := template.New(c.Heading).Parse(c.ValueTemplate)
		if err != nil {
			return nil, fmt.Errorf("column %q: %w", c.Heading, err)
		}
		rc := resolvedColumn{col: c, tmpl: t}
		if c.ShortValueTemplate != "" {
			st, err := template.New(c.Heading + "_short").Parse(c.ShortValueTemplate)
			if err != nil {
				return nil, fmt.Errorf("column %q short template: %w", c.Heading, err)
			}
			rc.shortTmpl = st
		}
		if c.CardValueTemplate != "" {
			ct, err := template.New(c.Heading + "_card").Parse(c.CardValueTemplate)
			if err != nil {
				return nil, fmt.Errorf("column %q card template: %w", c.Heading, err)
			}
			rc.cardTmpl = ct
		}
		parsed[i] = rc
	}
	return parsed, nil
}

var _ Formatter = (*PrettyTableFormatter)(nil)
