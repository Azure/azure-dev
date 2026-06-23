// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import "text/template"

// Width breakpoint constants for responsive layout selection.
const (
	// DefaultFullThreshold is the terminal width at or above which all columns
	// are shown with full text values.
	DefaultFullThreshold = 100

	// DefaultCompactThreshold is the terminal width at or above which only
	// high-priority columns (Priority ≤ compactPriorityThreshold) are shown.
	// Below this value the card layout is used.
	DefaultCompactThreshold = 60

	// columnPadding is the whitespace gap between columns (no box-drawing separators).
	columnPadding = 3

	// compactPriorityThreshold is the highest Priority value still shown at the
	// compact breakpoint. Columns with a larger Priority are dropped.
	compactPriorityThreshold = 2

	// minVisibleChars is the minimum number of characters shown before the
	// ellipsis when a value is truncated.
	minVisibleChars = 5

	// maxWrapLines is the maximum number of lines a Wrappable cell may occupy
	// before its content is truncated.
	maxWrapLines = 2

	// ellipsis is appended to a value that has been shortened to fit.
	ellipsis = "…"

	// hiddenColumnHeading is the header-only placeholder column rendered at the
	// compact breakpoint to signal that lower-priority columns were hidden.
	hiddenColumnHeading = "···"
)

// breakpoint identifies which responsive layout the formatter renders.
type breakpoint int

const (
	// breakpointCard renders one card per row (optionally grouped).
	breakpointCard breakpoint = iota
	// breakpointCompact renders a reduced set of columns.
	breakpointCompact
	// breakpointFull renders every column.
	breakpointFull
)

// PrettyColumn extends Column with responsive layout metadata.
type PrettyColumn struct {
	Column

	// Priority controls column visibility at the compact breakpoint.
	// 1 and 2 are shown at compact; 3+ are full-table only. 0 is treated as 1.
	Priority int

	// ShortValueTemplate is an alternative Go template used at the compact
	// breakpoint. If empty, the regular ValueTemplate is used.
	ShortValueTemplate string

	// CardValueTemplate is an alternative Go template used by the card layout.
	// If empty, the regular ValueTemplate is used. It allows a column to omit
	// redundant values in cards (e.g. a "latest" version that equals the
	// installed one) while still rendering them in the table layouts.
	CardValueTemplate string

	// ColorFunc applies color formatting to the cell value. If nil, no color
	// formatting is applied.
	ColorFunc func(string) string

	// Truncatable allows the value to be shortened with an ellipsis to fit the
	// available width. At least minVisibleChars characters are kept.
	Truncatable bool

	// Wrappable allows the value to wrap onto at most maxWrapLines lines before
	// being truncated. Used for identifier columns (e.g. NAME) where wrapping is
	// preferred over truncation.
	Wrappable bool

	// CardTitle renders this column's value as the card's bold, highlighted
	// title line (without a label) in the card layout, excluding it from the
	// labeled card fields.
	CardTitle bool

	// AlignLeadingSymbol left-pads values that lack a leading "symbol + space"
	// prefix (e.g. "⟳ Update available") so their text aligns with values that
	// have one. When no value in the column has a leading symbol, values are
	// left-aligned without padding. Applies to the table layouts only.
	AlignLeadingSymbol bool
}

// PrettyTableFormatterOptions configures the pretty table formatter.
type PrettyTableFormatterOptions struct {
	Columns []PrettyColumn

	// CardGroupColumn is the heading of the column to group cards by in the card
	// layout. Each distinct value becomes a section header. If empty, cards are
	// rendered ungrouped.
	CardGroupColumn string

	// FullThreshold is the terminal width at or above which the full layout is
	// used. Defaults to DefaultFullThreshold (100).
	FullThreshold int

	// CompactThreshold is the terminal width at or above which the compact
	// layout is used. Below this the card layout is used.
	// Defaults to DefaultCompactThreshold (60).
	CompactThreshold int

	// ResponsiveColumnHint enables the header-only "..." column and the
	// "Showing N of M columns" / "Resize the terminal..." hint message in the
	// table layouts when columns are hidden or values are truncated.
	ResponsiveColumnHint bool

	// ForceCards renders the card layout regardless of terminal width.
	ForceCards bool
}

// resolvedColumn pairs a PrettyColumn with its compiled templates.
type resolvedColumn struct {
	col       PrettyColumn
	tmpl      *template.Template
	shortTmpl *template.Template // nil when ShortValueTemplate is empty
	cardTmpl  *template.Template // nil when CardValueTemplate is empty
}

// priority returns the effective compact-visibility priority (0 maps to 1).
func (c resolvedColumn) priority() int {
	if c.col.Priority == 0 {
		return 1
	}
	return c.col.Priority
}
