// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"text/template"

	"github.com/fatih/color"
)

// Width breakpoint constants for responsive layout selection.
const (
	// DefaultWideThreshold is the terminal width at or above which all columns
	// are shown with full text values.
	DefaultWideThreshold = 110

	// DefaultMediumThreshold is the terminal width at or above which all columns
	// are shown but truncatable columns are shortened and ShortValueTemplate is used.
	DefaultMediumThreshold = 80

	// DefaultNarrowThreshold is the terminal width at or above which only
	// high-priority columns (Priority ≤ 2) are shown in a table.
	// Below this value the card/stacked layout is used.
	DefaultNarrowThreshold = 50

	// columnPadding is the whitespace gap between columns (no box-drawing separators).
	columnPadding = 3

	// minTruncLen is the minimum number of visible characters before adding "…".
	minTruncLen = 15
)

// PrettyColumn extends Column with priority for responsive column dropping.
type PrettyColumn struct {
	Column

	// Priority controls column visibility and drop order.
	// 1 = always shown, 2 = dropped third, 3 = dropped second, 4 = dropped first.
	// 0 is treated as 1 (always shown).
	Priority int

	// Truncatable indicates this column's values can be truncated with "…"
	// at medium/narrow widths to fit the terminal.
	Truncatable bool

	// ShortValueTemplate is an alternative Go template used at medium and narrow
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
	// in the very-narrow card layout. Each distinct value becomes a section header.
	// If empty, cards are rendered ungrouped with the first column as card title.
	CardGroupColumn string

	// WideThreshold is the terminal width at or above which the wide layout
	// is used (all columns, full text). Defaults to DefaultWideThreshold (110).
	WideThreshold int

	// MediumThreshold is the terminal width at or above which the medium layout
	// is used (all columns, truncated text). Defaults to DefaultMediumThreshold (80).
	MediumThreshold int

	// NarrowThreshold is the terminal width at or above which the narrow table
	// layout is used (Priority ≤ 2 columns only). Below this the card layout is used.
	// Defaults to DefaultNarrowThreshold (50).
	NarrowThreshold int
}

// parsedCol pairs a PrettyColumn with its compiled templates.
type parsedCol struct {
	col       PrettyColumn
	tmpl      *template.Template
	shortTmpl *template.Template // nil when ShortValueTemplate is empty
}

// PrettyTableFormatter renders tabular data with responsive breakpoints.
// It supports 4 layout modes based on terminal width: wide table, medium table
// (with truncation), narrow table (fewer columns), and card layout.
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
	for i, c := range options.Columns {
		t, err := template.New(c.Heading).Parse(c.ValueTemplate)
		if err != nil {
			return err
		}
		pc := parsedCol{col: c, tmpl: t}
		if c.ShortValueTemplate != "" {
			st, err := template.New(c.Heading + "_short").Parse(c.ShortValueTemplate)
			if err != nil {
				return err
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

	wideT := options.WideThreshold
	if wideT <= 0 {
		wideT = DefaultWideThreshold
	}
	medT := options.MediumThreshold
	if medT <= 0 {
		medT = DefaultMediumThreshold
	}
	narrowT := options.NarrowThreshold
	if narrowT <= 0 {
		narrowT = DefaultNarrowThreshold
	}

	switch {
	case termWidth >= wideT:
		return f.formatWideTable(parsed, rows, termWidth, writer)
	case termWidth >= medT:
		return f.formatMediumTable(parsed, rows, termWidth, writer)
	case termWidth >= narrowT:
		return f.formatNarrowTable(parsed, rows, termWidth, writer)
	default:
		return f.formatGroupedCards(parsed, rows, termWidth, writer, options)
	}
}

// formatWideTable renders all columns with full text values and whitespace padding.
func (f *PrettyTableFormatter) formatWideTable(
	parsed []parsedCol, rows []any, termWidth int, writer io.Writer,
) error {
	return f.renderPaddedTable(parsed, rows, termWidth, writer, false)
}

// formatMediumTable renders all columns; truncatable columns are shortened and
// ShortValueTemplate is used where available.
func (f *PrettyTableFormatter) formatMediumTable(
	parsed []parsedCol, rows []any, termWidth int, writer io.Writer,
) error {
	return f.renderPaddedTable(parsed, rows, termWidth, writer, true)
}

// formatNarrowTable renders only Priority ≤ 2 columns with ShortValueTemplate.
func (f *PrettyTableFormatter) formatNarrowTable(
	parsed []parsedCol, rows []any, termWidth int, writer io.Writer,
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
	return f.renderPaddedTable(filtered, rows, termWidth, writer, true)
}

// renderPaddedTable builds a whitespace-padded table with a header underline.
// When useShort is true, ShortValueTemplate and truncation are applied.
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
				return err
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
		colWidths[ci] = len(pc.col.Heading)
	}
	for _, rowVals := range grid.values {
		for ci, val := range rowVals {
			if len(val) > colWidths[ci] {
				colWidths[ci] = len(val)
			}
		}
	}

	// If useShort, truncate truncatable columns to fit within termWidth
	if useShort {
		totalWidth := sumWidths(colWidths) + (len(cols)-1)*columnPadding
		if totalWidth > termWidth {
			truncatableSpace := truncateColumnsToFit(cols, colWidths, totalWidth, termWidth)
			_ = truncatableSpace
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
		buf.WriteString(boldHeader.Sprintf("%-*s", colWidths[ci], heading))
	}
	buf.WriteByte('\n')

	// Header underline
	lineWidth := sumWidths(colWidths) + max(0, len(cols)-1)*columnPadding
	if lineWidth > termWidth {
		lineWidth = termWidth
	}
	buf.WriteString(strings.Repeat("─", lineWidth))
	buf.WriteByte('\n')

	// Data rows
	for ri, rowVals := range grid.values {
		for ci, val := range rowVals {
			if ci > 0 {
				buf.WriteString(strings.Repeat(" ", columnPadding))
			}
			// Truncate if needed
			displayVal := val
			if useShort && cols[ci].col.Truncatable && len(val) > colWidths[ci] {
				displayVal = truncateWithEllipsis(val, colWidths[ci])
			}

			// Apply color after truncation so ANSI codes don't get clipped
			colored := displayVal
			if cols[ci].col.ColorFunc != nil {
				colored = cols[ci].col.ColorFunc(displayVal)
			}

			// Pad based on plain-text width, not colored width
			padNeeded := colWidths[ci] - len(displayVal)
			if padNeeded < 0 {
				padNeeded = 0
			}
			buf.WriteString(colored)
			// Don't pad the last column
			if ci < len(cols)-1 {
				buf.WriteString(strings.Repeat(" ", padNeeded))
			}
		}
		// No trailing newline after the very last row — let the caller decide
		if ri < len(grid.values)-1 {
			buf.WriteByte('\n')
		} else {
			buf.WriteByte('\n')
		}
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
				return err
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
			headerText := "── " + group + " "
			remaining := termWidth - len(headerText)
			if remaining < 1 {
				remaining = 1
			}
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
					buf.WriteString(strings.Repeat(" ", padding+1))
					buf.WriteString(" ")
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

			borderWidth := termWidth - 2
			if borderWidth < 20 {
				borderWidth = 20
			}
			if borderWidth > 76 {
				borderWidth = 76
			}

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

// prettyExecTemplaterenders a template against a data row and returns the string result.
func prettyExecTemplate(tmpl *template.Template, data any) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// truncateWithEllipsis truncates s to maxLen characters, replacing the last char with "…".
func truncateWithEllipsis(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return "…"
	}
	return s[:maxLen-1] + "…"
}

// truncateColumnsToFit reduces colWidths for truncatable columns to make the
// total table width fit within termWidth. Returns total space reclaimed.
func truncateColumnsToFit(
	cols []parsedCol, colWidths []int, totalWidth, termWidth int,
) int {
	excess := totalWidth - termWidth
	if excess <= 0 {
		return 0
	}

	reclaimed := 0
	// Find truncatable columns sorted by current width descending (shrink widest first)
	type truncIdx struct {
		idx   int
		width int
	}
	var truncatable []truncIdx
	for i, pc := range cols {
		if pc.col.Truncatable && colWidths[i] > minTruncLen {
			truncatable = append(truncatable, truncIdx{idx: i, width: colWidths[i]})
		}
	}
	slices.SortFunc(truncatable, func(a, b truncIdx) int {
		return b.width - a.width // descending
	})

	for _, ti := range truncatable {
		if reclaimed >= excess {
			break
		}
		canShrink := colWidths[ti.idx] - minTruncLen
		needed := excess - reclaimed
		shrink := min(canShrink, needed)
		colWidths[ti.idx] -= shrink
		reclaimed += shrink
	}

	return reclaimed
}

// responsiveFilter drops the highest-Priority-number columns one at a time until
// the estimated table width fits within termWidth, or no more droppable columns remain.
func responsiveFilter(cols []parsedCol, rows []any, termWidth int) []parsedCol {
	visible := slices.Clone(cols)

	for {
		width := estimateTableWidth(visible, rows)
		if width <= termWidth {
			break
		}

		// Find the column with the highest priority number to drop first
		dropIdx := -1
		dropPriority := math.MinInt
		for i, pc := range visible {
			if pc.col.Priority > 0 && pc.col.Priority > dropPriority {
				dropPriority = pc.col.Priority
				dropIdx = i
			}
		}

		if dropIdx < 0 {
			break
		}

		visible = append(visible[:dropIdx], visible[dropIdx+1:]...)
	}

	return visible
}

// estimateTableWidth computes the minimum table width by measuring the max content
// width of each column plus inter-column padding.
func estimateTableWidth(cols []parsedCol, rows []any) int {
	colWidths := make([]int, len(cols))

	for i, pc := range cols {
		colWidths[i] = len(pc.col.Heading)
	}

	for _, row := range rows {
		for i, pc := range cols {
			val, err := prettyExecTemplate(pc.tmpl, row)
			if err != nil {
				continue
			}
			if len(val) > colWidths[i] {
				colWidths[i] = len(val)
			}
		}
	}

	total := sumWidths(colWidths)
	if len(cols) > 1 {
		total += (len(cols) - 1) * columnPadding
	}

	return total
}

func sumWidths(widths []int) int {
	total := 0
	for _, w := range widths {
		total += w
	}
	return total
}

var _ Formatter = (*PrettyTableFormatter)(nil)
