// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"io"
	"slices"
	"strings"
)

const (
	// primaryPrefix is the leading label of the first suggestion line.
	primaryPrefix = "Next:  "
	// continuationPrefix indents subsequent lines so commands align under
	// the first command. Width == len(primaryPrefix).
	continuationPrefix = "       "
	// commandSeparator separates the (possibly padded) command from its
	// description. Two-space gap + "-- " per the design spec.
	commandSeparator = "  -- "
	// maxRendered caps the block at one primary + one optional secondary
	// line ("more than two lines drowns out command output").
	maxRendered = 2
)

// PrintNext writes a "Next:" guidance block to w. Suggestions are sorted
// ascending by Priority (stable; ties preserve input order) and then
// truncated to a primary + optional secondary line. Empty input produces
// no output and no write.
//
// PrintNext does not inspect TTY state or output-format flags — those
// decisions live at the call site so the same renderer can serve both
// interactive stdout writes and string capture for tests / JSON envelopes.
func PrintNext(w io.Writer, suggestions []Suggestion) error {
	block := renderBlock(suggestions)
	if block == "" {
		return nil
	}
	_, err := io.WriteString(w, block)
	return err
}

// renderBlock returns the formatted "Next:" block (with a leading blank
// line and trailing newline) or an empty string when there is nothing to
// render.
func renderBlock(suggestions []Suggestion) string {
	if len(suggestions) == 0 {
		return ""
	}

	sorted := slices.Clone(suggestions)
	slices.SortStableFunc(sorted, func(a, b Suggestion) int {
		return a.Priority - b.Priority
	})
	if len(sorted) > maxRendered {
		sorted = sorted[:maxRendered]
	}

	cmdWidth := 0
	for _, s := range sorted {
		if n := len(s.Command); n > cmdWidth {
			cmdWidth = n
		}
	}

	var b strings.Builder
	// Leading blank line separates the block from preceding output.
	b.WriteByte('\n')
	for i, s := range sorted {
		if i == 0 {
			b.WriteString(primaryPrefix)
		} else {
			b.WriteString(continuationPrefix)
		}
		b.WriteString(s.Command)
		if pad := cmdWidth - len(s.Command); pad > 0 {
			b.WriteString(strings.Repeat(" ", pad))
		}
		b.WriteString(commandSeparator)
		b.WriteString(s.Description)
		b.WriteByte('\n')
	}
	return b.String()
}
