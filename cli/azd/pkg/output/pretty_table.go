// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"text/template"

	"github.com/fatih/color"
	"github.com/mattn/go-runewidth"
)

// Width breakpoint constants for responsive layout selection.
const (
	// DefaultFullThreshold is the terminal width at or above which all columns
	// are shown with full text values.
	DefaultFullThreshold = 100

	// DefaultCompactThreshold is the terminal width at or above which only
	// high-priority columns (Priority ≤ 2) are shown with ShortValueTemplate.
	// Below this value the card layout is used.
	DefaultCompactThreshold = 60

	// columnPadding is the whitespace gap between columns (no box-drawing separators).
	columnPadding = 3
)

// PrettyColumn extends Column with priority for responsive column dropping.
type PrettyColumn struct {
	Column

	// Priority controls column visibility at compact widths.
	// 1 = always shown in compact, 2 = always shown in compact,
	// 3+ = full table only. 0 is treated as 1.
	Priority int

	// ShortValueTemplate is an alternative Go template used at compact
	// widths. If empty, the regular ValueTemplate is used.
	ShortValueTemplate string

	// ColorFunc applies color formatting to the cell value.
	// If nil, no color formatting is applied.
	ColorFunc func(string) string
}

// PrettyTableFormatterOptions configures the pretty table formatter.
type PrettyTableFormatterOptions struct {
	Columns []PrettyColumn

	// CardGroupColumn is the heading name of the column to group cards by
	// in the card layout. Each distinct value becomes a section header.
	// If empty, cards are rendered ungrouped with the first column as card title.
	CardGroupColumn string

	// FullThreshold is the terminal width at or above which the full layout
	// is used (all columns, full text). Defaults to DefaultFullThreshold (100).
	FullThreshold int

	// CompactThreshold is the terminal width at or above which the compact
	// layout is used (Priority ≤ 2 columns, ShortValueTemplate).
	// Below this the card layout is used.
	// Defaults to DefaultCompactThreshold (60).
	CompactThreshold int
}

// parsedCol pairs a PrettyColumn with its compiled templates.
type parsedCol struct {
	col       PrettyColumn
	tmpl      *template.Template
	shortTmpl *template.Template // nil when ShortValueTemplate is empty
}

// PrettyTableFormatter renders tabular data with responsive breakpoints.
// It supports 3 layout modes based on terminal width: full table,
// compact table (fewer columns), and card layout.
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

	// Parse templates upfront
	parsed := make([]parsedCol, len(options.Columns))
	seenHeadings := make(map[string]bool, len(options.Columns))
	for i, c := range options.Columns {
		if seenHeadings[c.Heading] {
			return fmt.Errorf("duplicate column heading %q", c.Heading)
		}
		seenHeadings[c.Heading] = true

		t, err := template.New(c.Heading).Parse(c.ValueTemplate)
		if err != nil {
			return fmt.Errorf("column %q: %w", c.Heading, err)
		}
		pc := parsedCol{col: c, tmpl: t}
		if c.ShortValueTemplate != "" {
			st, err := template.New(c.Heading + "_short").Parse(c.ShortValueTemplate)
			if err != nil {
				return fmt.Errorf("column %q short template: %w", c.Heading, err)
			}
			pc.shortTmpl = st
		}
		parsed[i] = pc
	}

	widthFn := f.ConsoleWidthFn
	if widthFn == nil {
		widthFn = getConsoleWidth
	}
	termWidth := widthFn()

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
		return f.renderFullTable(parsed, rows, termWidth, writer)
	case termWidth >= compactT:
		return f.renderCompactTable(parsed, rows, termWidth, writer, options)
	default:
		return f.formatGroupedCards(parsed, rows, termWidth, writer, options)
	}
}

// renderFullTable renders all columns with full text values and whitespace padding.
func (f *PrettyTableFormatter) renderFullTable(
	parsed []parsedCol, rows []any, termWidth int, writer io.Writer,
) error {
	return f.renderPaddedTable(parsed, rows, termWidth, writer, false)
}

// renderCompactTable renders only Priority ≤ 2 columns with ShortValueTemplate.
func (f *PrettyTableFormatter) renderCompactTable(
	parsed []parsedCol, rows []any, termWidth int, writer io.Writer, options PrettyTableFormatterOptions,
) error {
	filtered := make([]parsedCol, 0, len(parsed))
	for _, pc := range parsed {
		p := pc.col.Priority
		if p == 0 {
			p = 1
		}
		if p <= 2 {
			filtered = append(filtered, pc)
		}
	}
	if len(filtered) == 0 {
		return f.formatGroupedCards(parsed, rows, termWidth, writer, options)
	}
	return f.renderPaddedTable(filtered, rows, termWidth, writer, true)
}

// renderPaddedTable builds a whitespace-padded table with a header underline.
// When useShort is true, ShortValueTemplate is used where available.
func (f *PrettyTableFormatter) renderPaddedTable(
	cols []parsedCol, rows []any, termWidth int, writer io.Writer, useShort bool,
) error {
	// Resolve cell values
	type cellGrid struct {
		values [][]string // [row][col]
	}
	grid := cellGrid{values: make([][]string, len(rows))}

	for ri, row := range rows {
		grid.values[ri] = make([]string, len(cols))
		for ci, pc := range cols {
			tmpl := pc.tmpl
			if useShort && pc.shortTmpl != nil {
				tmpl = pc.shortTmpl
			}
			val, err := prettyExecTemplate(tmpl, row)
			if err != nil {
				return fmt.Errorf("row %d, column %q: %w", ri, pc.col.Heading, err)
			}
			if pc.col.Transformer != nil {
				val = pc.col.Transformer(val)
			}
			grid.values[ri][ci] = val
		}
	}

	// Compute natural column widths (max of heading and all cell values)
	colWidths := make([]int, len(cols))
	for ci, pc := range cols {
		colWidths[ci] = displayWidth(pc.col.Heading)
	}
	for _, rowVals := range grid.values {
		for ci, val := range rowVals {
			if dw := displayWidth(val); dw > colWidths[ci] {
				colWidths[ci] = dw
			}
		}
	}

	boldHeader := color.New(color.Bold, color.FgHiWhite)
	var buf bytes.Buffer

	// Header row
	for ci, pc := range cols {
		if ci > 0 {
			buf.WriteString(strings.Repeat(" ", columnPadding))
		}
		heading := pc.col.Heading
		hdw := displayWidth(heading)
		padNeeded := max(colWidths[ci]-hdw, 0)
		buf.WriteString(boldHeader.Sprint(heading))
		if ci < len(cols)-1 {
			buf.WriteString(strings.Repeat(" ", padNeeded))
		}
	}
	buf.WriteByte('\n')

	// Header underline
	lineWidth := min(sumWidths(colWidths)+max(0, len(cols)-1)*columnPadding, termWidth)
	buf.WriteString(strings.Repeat("─", lineWidth))
	buf.WriteByte('\n')

	// Data rows
	for _, rowVals := range grid.values {
		for ci, val := range rowVals {
			if ci > 0 {
				buf.WriteString(strings.Repeat(" ", columnPadding))
			}

			// Apply color
			colored := val
			if cols[ci].col.ColorFunc != nil {
				colored = cols[ci].col.ColorFunc(val)
			}

			// Pad based on display width, not byte length
			padNeeded := max(colWidths[ci]-displayWidth(val), 0)
			buf.WriteString(colored)
			// Don't pad the last column
			if ci < len(cols)-1 {
				buf.WriteString(strings.Repeat(" ", padNeeded))
			}
		}
		buf.WriteByte('\n')
	}

	_, err := fmt.Fprint(writer, buf.String())
	return err
}

// formatGroupedCards renders rows as cards grouped by CardGroupColumn.
// If CardGroupColumn is empty, cards are rendered ungrouped.
func (f *PrettyTableFormatter) formatGroupedCards(
	parsed []parsedCol, rows []any, termWidth int, writer io.Writer, options PrettyTableFormatterOptions,
) error {
	groupColIdx := -1
	if options.CardGroupColumn != "" {
		for i, pc := range parsed {
			if pc.col.Heading == options.CardGroupColumn {
				groupColIdx = i
				break
			}
		}
		if groupColIdx < 0 {
			return fmt.Errorf("CardGroupColumn %q does not match any column heading", options.CardGroupColumn)
		}
	}

	// Resolve all row values
	type rowData struct {
		values   map[string]string // heading -> value
		groupVal string
	}
	allRows := make([]rowData, len(rows))
	for ri, row := range rows {
		rd := rowData{values: make(map[string]string)}
		for _, pc := range parsed {
			val, err := prettyExecTemplate(pc.tmpl, row)
			if err != nil {
				return fmt.Errorf("row %d, column %q: %w", ri, pc.col.Heading, err)
			}
			if pc.col.Transformer != nil {
				val = pc.col.Transformer(val)
			}
			if pc.col.ColorFunc != nil {
				rd.values[pc.col.Heading+"_colored"] = pc.col.ColorFunc(val)
			}
			rd.values[pc.col.Heading] = val
		}
		if groupColIdx >= 0 {
			rd.groupVal = rd.values[parsed[groupColIdx].col.Heading]
		}
		allRows[ri] = rd
	}

	// Determine card field order — exclude group column from card body
	type cardField struct {
		heading  string
		hasColor bool
	}
	var cardFields []cardField
	for _, pc := range parsed {
		if groupColIdx >= 0 && pc.col.Heading == parsed[groupColIdx].col.Heading {
			continue
		}
		cardFields = append(cardFields, cardField{
			heading:  pc.col.Heading,
			hasColor: pc.col.ColorFunc != nil,
		})
	}

	// Find max heading length for alignment
	maxHeadingLen := 0
	for _, cf := range cardFields {
		if len(cf.heading) > maxHeadingLen {
			maxHeadingLen = len(cf.heading)
		}
	}

	var buf bytes.Buffer

	if groupColIdx >= 0 {
		// Grouped cards: collect distinct group values in order
		var groupOrder []string
		groupRows := make(map[string][]rowData)
		for _, rd := range allRows {
			if _, exists := groupRows[rd.groupVal]; !exists {
				groupOrder = append(groupOrder, rd.groupVal)
			}
			groupRows[rd.groupVal] = append(groupRows[rd.groupVal], rd)
		}

		for gi, group := range groupOrder {
			// Group header: "── {value} ────────"
			// Strip ANSI codes from group value — it comes from template execution
			// and may contain color codes if the column has a ColorFunc.
			strippedGroup := ansiRegex.ReplaceAllString(group, "")
			headerText := "── " + strippedGroup + " "
			remaining := max(termWidth-displayWidth(headerText), 1)
			buf.WriteString(headerText)
			buf.WriteString(strings.Repeat("─", remaining))
			buf.WriteByte('\n')
			buf.WriteByte('\n')

			for ri, rd := range groupRows[group] {
				for _, cf := range cardFields {
					val := rd.values[cf.heading]
					if val == "" {
						continue
					}
					displayVal := val
					if cf.hasColor {
						if colored, ok := rd.values[cf.heading+"_colored"]; ok {
							displayVal = colored
						}
					}
					padding := maxHeadingLen - len(cf.heading)
					buf.WriteString(cf.heading)
					buf.WriteString(":")
					buf.WriteString(strings.Repeat(" ", padding+2))
					buf.WriteString(displayVal)
					buf.WriteByte('\n')
				}
				if ri < len(groupRows[group])-1 {
					buf.WriteByte('\n')
				}
			}

			if gi < len(groupOrder)-1 {
				buf.WriteByte('\n')
			}
		}
	} else {
		// Ungrouped cards — use first column as card title
		boldTitle := color.New(color.Bold, color.FgHiWhite)

		for ri, rd := range allRows {
			titleHeading := parsed[0].col.Heading
			titleVal := rd.values[titleHeading]

			borderWidth := min(max(termWidth-2, 20), 76)

			buf.WriteString("┌" + strings.Repeat("─", borderWidth))
			buf.WriteByte('\n')
			buf.WriteString("│ ")
			buf.WriteString(boldTitle.Sprint(titleVal))
			buf.WriteByte('\n')

			for _, cf := range cardFields {
				if cf.heading == titleHeading {
					continue
				}
				val := rd.values[cf.heading]
				if val == "" {
					continue
				}
				if cf.hasColor {
					if colored, ok := rd.values[cf.heading+"_colored"]; ok {
						val = colored
					}
				}
				padding := maxHeadingLen - len(cf.heading)
				buf.WriteString("│ ")
				buf.WriteString(cf.heading)
				buf.WriteString(": ")
				buf.WriteString(strings.Repeat(" ", padding))
				buf.WriteString(val)
				buf.WriteByte('\n')
			}

			buf.WriteString("└" + strings.Repeat("─", borderWidth))
			buf.WriteByte('\n')
			if ri < len(allRows)-1 {
				buf.WriteByte('\n')
			}
		}
	}

	_, err := fmt.Fprint(writer, buf.String())
	return err
}

// prettyExecTemplate renders a template against a data row and returns the string result.
func prettyExecTemplate(tmpl *template.Template, data any) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func sumWidths(widths []int) int {
	total := 0
	for _, w := range widths {
		total += w
	}
	return total
}

// ansiRegex matches ANSI escape codes used for terminal coloring.
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// displayWidth returns the visible column width of s, ignoring ANSI escape
// codes and accounting for wide Unicode characters (e.g. CJK).
func displayWidth(s string) int {
	stripped := ansiRegex.ReplaceAllString(s, "")
	return runewidth.StringWidth(stripped)
}

var _ Formatter = (*PrettyTableFormatter)(nil)
