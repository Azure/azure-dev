// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/fatih/color"
)

// renderTable renders cols and rows as a whitespace-padded table with a header
// underline. It returns the rendered table and whether any value was truncated
// to fit.
func (f *PrettyTableFormatter) renderTable(
	cols []resolvedColumn, rows []any, termWidth int,
) (string, bool, error) {
	// Resolve plain cell values.
	plain := make([][]string, len(rows))
	for ri, row := range rows {
		plain[ri] = make([]string, len(cols))
		for ci, rc := range cols {
			val, err := prettyExecTemplate(rc.tmpl, row)
			if err != nil {
				return "", false, fmt.Errorf("row %d, column %q: %w", ri, rc.col.Heading, err)
			}
			if rc.col.Transformer != nil {
				val = rc.col.Transformer(val)
			}
			plain[ri][ci] = val
		}
	}

	// Natural width = max of heading and all cell values.
	natural := make([]int, len(cols))
	for ci, rc := range cols {
		natural[ci] = displayWidth(rc.col.Heading)
	}
	for _, rowVals := range plain {
		for ci, val := range rowVals {
			natural[ci] = max(natural[ci], displayWidth(val))
		}
	}

	widths := fitColumns(cols, natural, termWidth)

	// Lay out cells into (possibly multiple) colored lines, tracking truncation.
	type cell struct {
		lines []string
	}
	grid := make([][]cell, len(rows))
	truncated := false
	for ri, rowVals := range plain {
		grid[ri] = make([]cell, len(cols))
		for ci, val := range rowVals {
			lines, lost := layoutCell(val, widths[ci], cols[ci].col.Wrappable)
			truncated = truncated || lost
			if cf := cols[ci].col.ColorFunc; cf != nil {
				for li, ln := range lines {
					if ln != "" {
						// Classify the color from the full cell value, but apply it
						// to the visible (possibly truncated) line so that a
						// truncated value keeps the color of its underlying value.
						lines[li] = colorLineLikeValue(cf, val, ln)
					}
				}
			}
			grid[ri][ci] = cell{lines: lines}
		}
	}

	var buf bytes.Buffer
	boldHeader := color.New(color.Bold, color.FgHiWhite)

	// Header row.
	for ci, rc := range cols {
		if ci > 0 {
			buf.WriteString(strings.Repeat(" ", columnPadding))
		}
		buf.WriteString(boldHeader.Sprint(rc.col.Heading))
		if ci < len(cols)-1 {
			buf.WriteString(strings.Repeat(" ", max(widths[ci]-displayWidth(rc.col.Heading), 0)))
		}
	}
	buf.WriteByte('\n')

	// Header underline.
	lineWidth := min(rowWidth(widths), termWidth)
	buf.WriteString(WithGrayFormat(strings.Repeat("─", lineWidth)))
	buf.WriteByte('\n')

	// Data rows, rendered line by line so wrapped cells expand the row height.
	for ri := range rows {
		height := 1
		for ci := range cols {
			height = max(height, len(grid[ri][ci].lines))
		}
		for li := range height {
			for ci := range cols {
				if ci > 0 {
					buf.WriteString(strings.Repeat(" ", columnPadding))
				}
				line := ""
				if li < len(grid[ri][ci].lines) {
					line = grid[ri][ci].lines[li]
				}
				buf.WriteString(line)
				if ci < len(cols)-1 {
					buf.WriteString(strings.Repeat(" ", max(widths[ci]-displayWidth(line), 0)))
				}
			}
			buf.WriteByte('\n')
		}
	}

	return buf.String(), truncated, nil
}

// renderFullTable renders every column with full text values.
func (f *PrettyTableFormatter) renderFullTable(
	parsed []resolvedColumn, rows []any, termWidth int, writer io.Writer,
	opts PrettyTableFormatterOptions,
) error {
	out, truncated, err := f.renderTable(parsed, rows, termWidth)
	if err != nil {
		return err
	}
	if _, err := writer.Write([]byte(out)); err != nil {
		return err
	}
	if opts.ResponsiveColumnHint && truncated {
		return writeColumnHint(writer, buildColumnHint(len(parsed), len(parsed), true))
	}
	return nil
}

// renderCompactTable renders only the columns visible at the compact breakpoint
// (Priority ≤ compactPriorityThreshold). When ResponsiveColumnHint is set and
// columns are dropped, a header-only "···" column and a hint message are added.
func (f *PrettyTableFormatter) renderCompactTable(
	parsed []resolvedColumn, rows []any, termWidth int,
	writer io.Writer, opts PrettyTableFormatterOptions,
) error {
	visible := make([]resolvedColumn, 0, len(parsed))
	for _, rc := range parsed {
		if rc.priority() <= compactPriorityThreshold {
			visible = append(visible, rc)
		}
	}
	if len(visible) == 0 {
		return f.formatGroupedCards(parsed, rows, termWidth, writer, opts)
	}

	dropped := len(parsed) - len(visible)
	cols := visible
	if opts.ResponsiveColumnHint && dropped > 0 {
		cols = append(cols, hiddenColumnPlaceholder())
	}

	out, truncated, err := f.renderTable(cols, rows, termWidth)
	if err != nil {
		return err
	}
	if _, err := writer.Write([]byte(out)); err != nil {
		return err
	}
	if opts.ResponsiveColumnHint {
		if hint := buildColumnHint(len(visible), len(parsed), truncated); hint != "" {
			return writeColumnHint(writer, hint)
		}
	}
	return nil
}

// hiddenColumnPlaceholder returns a header-only "···" column with no values,
// used to signal that lower-priority columns were hidden at compact width.
func hiddenColumnPlaceholder() resolvedColumn {
	// An empty template renders nothing for every row.
	tmpl := emptyTemplate
	return resolvedColumn{
		col:  PrettyColumn{Column: Column{Heading: hiddenColumnHeading, ValueTemplate: ""}},
		tmpl: tmpl,
	}
}

// writeColumnHint writes a blank separator line followed by the hint message.
func writeColumnHint(writer io.Writer, hint string) error {
	_, err := writer.Write([]byte("\n" + hint + "\n"))
	return err
}

// colorLineLikeValue applies the color that colorFunc assigns to the full cell
// value to a single (possibly truncated) display line. Color classification
// stays based on value while the visible text is line, so a truncated cell
// keeps the color of its underlying value instead of being reclassified from
// the truncated text (e.g. a shortened "Update available" status must stay
// warning-colored).
func colorLineLikeValue(colorFunc func(string) string, value, line string) string {
	colored := colorFunc(value)
	if colored == value {
		// No color applied (e.g. NO_COLOR is set); nothing to transfer.
		return line
	}
	if line == value {
		return colored
	}
	// Transfer the leading/trailing escape sequences that wrap the value onto
	// the visible line. colorFunc embeds value verbatim between a leading SGR
	// prefix and a trailing reset.
	if before, after, ok := strings.Cut(colored, value); ok {
		return before + line + after
	}
	// Fallback: color the line directly.
	return colorFunc(line)
}
