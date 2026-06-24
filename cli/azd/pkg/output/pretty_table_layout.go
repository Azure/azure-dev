// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"text/template"

	"github.com/mattn/go-runewidth"
)

// ansiRegex matches ANSI escape codes used for terminal coloring.
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// osc8Regex matches OSC-8 hyperlink escape sequences.
// Format: \x1b]8;params;URI\x1b\  (ST terminator) or \x1b]8;params;URI\a (BEL terminator)
var osc8Regex = regexp.MustCompile(`\x1b\]8;[^;]*;[^\x1b\a]*(?:\x1b\\|\a)`)

// displayWidth returns the visible column width of s, ignoring ANSI escape
// codes, OSC-8 hyperlink sequences, and accounting for wide Unicode characters.
func displayWidth(s string) int {
	return runewidth.StringWidth(stripTerminalEscapes(s))
}

// stripTerminalEscapes removes OSC-8 hyperlink and ANSI color escape sequences.
func stripTerminalEscapes(s string) string {
	stripped := osc8Regex.ReplaceAllString(s, "")
	return ansiRegex.ReplaceAllString(stripped, "")
}

// hasTerminalEscapes reports whether s contains ANSI or OSC-8 escape sequences.
func hasTerminalEscapes(s string) bool {
	return ansiRegex.MatchString(s) || osc8Regex.MatchString(s)
}

func sumWidths(widths []int) int {
	total := 0
	for _, w := range widths {
		total += w
	}
	return total
}

// prettyExecTemplate renders a template against a data row and returns the string result.
func prettyExecTemplate(tmpl *template.Template, data any) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// rowWidth returns the total rendered width of a row given per-column widths,
// including the padding between columns.
func rowWidth(widths []int) int {
	if len(widths) == 0 {
		return 0
	}
	return sumWidths(widths) + (len(widths)-1)*columnPadding
}

// columnFloor returns the minimum width a column may be shrunk to. Non-shrinkable
// columns return their natural width. The floor never drops below the heading
// width, so headings are never truncated.
func columnFloor(c resolvedColumn, natural int) int {
	headingWidth := displayWidth(c.col.Heading)
	switch {
	case c.col.Truncatable:
		// A truncatable column can shrink to a few characters plus the ellipsis.
		// If it is also wrappable, it wraps across maxWrapLines lines and then
		// truncates within them, so it can still reach this small floor — which
		// lets it yield space to columns that cannot truncate.
		target := minVisibleChars + displayWidth(ellipsis)
		return clampWidth(target, headingWidth, natural)
	case c.col.Wrappable:
		// A wrap-only column spreads its content across maxWrapLines lines, so it
		// needs roughly natural/maxWrapLines width to show everything.
		target := (natural + maxWrapLines - 1) / maxWrapLines
		return clampWidth(target, headingWidth, natural)
	default:
		return natural
	}
}

// clampWidth returns target bounded to [floor, ceil], assuming floor ≤ ceil.
func clampWidth(target, floor, ceil int) int {
	return min(max(target, floor), ceil)
}

// fitColumns assigns a rendered width to each column so the row fits termWidth.
// Overflow is reclaimed from shrinkable columns (Truncatable or Wrappable),
// shrinking the least-important columns first (highest Priority, then right-most).
// Non-shrinkable columns keep their natural width. The returned widths are never
// smaller than each column's floor; if the row still overflows, it is left to
// overflow rather than truncating headings.
func fitColumns(cols []resolvedColumn, natural []int, termWidth int) []int {
	widths := make([]int, len(natural))
	copy(widths, natural)

	if rowWidth(widths) <= termWidth || len(cols) == 0 {
		return widths
	}

	order := shrinkOrder(cols)
	for _, ci := range order {
		if rowWidth(widths) <= termWidth {
			break
		}
		floor := columnFloor(cols[ci], natural[ci])
		if widths[ci] <= floor {
			continue
		}
		over := rowWidth(widths) - termWidth
		widths[ci] = max(widths[ci]-over, floor)
	}

	return widths
}

// shrinkOrder returns the indices of shrinkable columns in the order they should
// be shrunk: least important first (highest Priority), ties broken right-to-left.
func shrinkOrder(cols []resolvedColumn) []int {
	order := make([]int, 0, len(cols))
	for i, c := range cols {
		if c.col.Truncatable || c.col.Wrappable {
			order = append(order, i)
		}
	}
	// Stable sort: higher priority value first; equal priority right-most first.
	for i := 1; i < len(order); i++ {
		for j := i; j > 0; j-- {
			a, b := order[j-1], order[j]
			swap := cols[a].priority() < cols[b].priority() ||
				(cols[a].priority() == cols[b].priority() && a < b)
			if !swap {
				break
			}
			order[j-1], order[j] = order[j], order[j-1]
		}
	}
	return order
}

// layoutCell returns the display lines for a value rendered within width and
// reports whether content was lost (truncated). Wrappable values wrap onto at
// most maxWrapLines lines before truncating; others render on a single line,
// truncating with an ellipsis when needed.
//
// Values that contain terminal escape sequences (ANSI color or OSC-8
// hyperlinks) are returned unchanged: truncating mid-sequence would corrupt the
// output, and escape-bearing columns are only configured as non-shrinkable
// (e.g. LOCATION links), so they are never assigned a width below their content
// and this branch is not reached in practice. Mark such a column Truncatable
// only after teaching this function to truncate while preserving escapes.
func layoutCell(value string, width int, wrappable bool) ([]string, bool) {
	if value == "" {
		return []string{""}, false
	}
	if displayWidth(value) <= width || hasTerminalEscapes(value) || width <= 0 {
		return []string{value}, false
	}
	if wrappable {
		return wrapValue(value, width, maxWrapLines)
	}
	return []string{truncateWithEllipsis(value, width)}, true
}

// truncateWithEllipsis shortens s to fit within maxWidth display columns,
// appending an ellipsis. At least minVisibleChars characters are kept.
func truncateWithEllipsis(s string, maxWidth int) string {
	if displayWidth(s) <= maxWidth {
		return s
	}
	keep := max(maxWidth-displayWidth(ellipsis), minVisibleChars)
	return takeWidth(s, keep) + ellipsis
}

// wrapValue word-wraps s onto at most maxLines lines of the given width. If
// content remains after maxLines lines, the last line is truncated with an
// ellipsis. It reports whether any content was lost.
func wrapValue(s string, width, maxLines int) ([]string, bool) {
	if maxLines <= 0 {
		return []string{""}, true
	}
	if width <= 0 {
		return []string{s}, true
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}, false
	}

	all := wrapWords(words, width)
	if len(all) <= maxLines {
		return all, false
	}

	// Keep the first maxLines lines and mark the last one as truncated so the
	// dropped content is visibly indicated.
	kept := all[:maxLines:maxLines]
	last := kept[len(kept)-1]
	kept[len(kept)-1] = truncateWithEllipsis(last+" "+ellipsis, width)
	return kept, true
}

// wrapWords greedily wraps words into lines no wider than width, hard-splitting
// any single word that exceeds width. It applies no line limit; callers cap the
// result as needed.
func wrapWords(words []string, width int) []string {
	var lines []string
	var current string
	for _, word := range words {
		// Hard-split a word that is wider than a full line into width-sized chunks.
		for displayWidth(word) > width {
			if current != "" {
				lines = append(lines, current)
				current = ""
			}
			head := takeWidth(word, width)
			if head == "" {
				// width is too narrow for even the first rune (e.g. width 1 with a
				// width-2 rune). Take one rune anyway so the loop makes progress;
				// the cell overflows by at most one rune, which only happens at
				// degenerate widths.
				head = string([]rune(word)[0])
			}
			lines = append(lines, head)
			word = word[len(head):]
		}
		if word == "" {
			continue
		}
		switch {
		case current == "":
			current = word
		case displayWidth(current)+1+displayWidth(word) <= width:
			current += " " + word
		default:
			lines = append(lines, current)
			current = word
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

// takeWidth returns the longest prefix of s whose display width is ≤ width.
func takeWidth(s string, width int) string {
	if width <= 0 {
		return ""
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if used+rw > width {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String()
}

// buildColumnHint returns the responsive hint message shown after a table when
// columns are hidden or values are truncated, or "" when no hint is needed.
// When columns are hidden it is prefixed with "Showing N of M columns.".
func buildColumnHint(visible, total int, truncated bool) string {
	hidden := total - visible
	if hidden <= 0 && !truncated {
		return ""
	}
	resize := fmt.Sprintf(
		"Resize the terminal or run with %s for full details.",
		WithHighLightFormat("-o json"),
	)
	if hidden > 0 {
		return fmt.Sprintf("Showing %d of %d columns. %s", visible, total, resize)
	}
	return resize
}
