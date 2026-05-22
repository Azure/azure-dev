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
//
// Use PrintAllNext when the resolver produces multiple REQUIRED follow-up
// actions (init / doctor fix-ups) where silently dropping any of them
// would mislead the user.
func PrintNext(w io.Writer, suggestions []Suggestion) error {
	block := renderBlock(suggestions, maxRendered)
	if block == "" {
		return nil
	}
	_, err := io.WriteString(w, block)
	return err
}

// PrintAllNext writes a "Next:" guidance block to w like PrintNext but
// renders every suggestion (no two-line cap). Use this for flows where
// the suggestions are all REQUIRED follow-up actions rather than
// alternatives — the post-init flow can surface unresolved manifest
// placeholders, missing `azd env set` keys, AND the trailing
// `azd deploy` reminder simultaneously, and the user has to act on each
// one. Dropping any of them silently leaves the user thinking they are
// ready to deploy when they are not.
//
// Suggestions are still stable-sorted by Priority (ties preserve input
// order), the Trailing entry is still rendered last, and framing
// (leading blank line + trailing newline) matches PrintNext. Empty
// input is a no-op.
func PrintAllNext(w io.Writer, suggestions []Suggestion) error {
	block := renderBlock(suggestions, 0)
	if block == "" {
		return nil
	}
	_, err := io.WriteString(w, block)
	return err
}

// FormatNextForNote renders a "Next:" block as a string suitable for
// embedding in an artifact's Metadata["note"]. Unlike PrintNext it does
// not truncate the block (the artifact note is a contained region, not
// interleaved with command output) and does not include a leading or
// trailing newline (the artifact renderer adds its own line break).
//
// Lines 2+ are pre-indented by 4 spaces so the command column stays
// aligned with line 1 when core azd's artifact renderer (which only
// indents the first line of the note) is called with the typical caller
// indent of two spaces — see cli/azd/pkg/project/artifact.go, which
// writes "\n%s  %s" with the caller's indent on line 1 only. Under
// deeper or shallower caller indents the lines drift slightly but the
// note remains readable in both cases.
//
// Empty input returns an empty string.
func FormatNextForNote(suggestions []Suggestion) string {
	body := renderRows(suggestions, 0)
	if body == "" {
		return ""
	}
	return strings.ReplaceAll(strings.TrimSuffix(body, "\n"), "\n", "\n    ")
}

// renderBlock returns the formatted "Next:" block (with a leading blank
// line and trailing newline) or an empty string when there is nothing to
// render. limit is forwarded to renderRows: a positive value caps the
// block at that many visible lines (PrintNext default), while limit <= 0
// renders every suggestion (PrintAllNext).
func renderBlock(suggestions []Suggestion, limit int) string {
	body := renderRows(suggestions, limit)
	if body == "" {
		return ""
	}
	// Leading blank line separates the block from preceding output.
	return "\n" + body
}

// renderRows returns the formatted suggestion lines (one per line,
// terminated with "\n") with no leading blank line. limit caps the
// number of visible suggestions; limit <= 0 means render every
// suggestion.
//
// Truncation is partitioned: at most one Suggestion.Trailing entry is
// reserved for the final visible slot, with remaining slots filled by
// primary (non-trailing) entries in ascending Priority order. The
// trailing reservation lets resolvers emit follow-up nudges (e.g., the
// post-action `azd deploy` line) without having those nudges silently
// dropped when primary suggestions outnumber the cap.
func renderRows(suggestions []Suggestion, limit int) string {
	if len(suggestions) == 0 {
		return ""
	}

	sorted := slices.Clone(suggestions)
	slices.SortStableFunc(sorted, func(a, b Suggestion) int {
		return a.Priority - b.Priority
	})

	var primary []Suggestion
	var trailing *Suggestion
	for i := range sorted {
		if sorted[i].Trailing {
			// Always overwrite: because sorted is ascending by Priority,
			// the last Trailing entry encountered has the highest
			// Priority — i.e. the most-deferred footer wins on
			// collision, defending the intended `azd deploy` slot from
			// accidental lower-Priority Trailing flags.
			trailing = &sorted[i]
			continue
		}
		primary = append(primary, sorted[i])
	}

	var rendered []Suggestion
	if limit > 0 && trailing != nil {
		budget := max(limit-1, 0)
		if len(primary) > budget {
			primary = primary[:budget]
		}
		rendered = append(primary, *trailing)
	} else if limit > 0 {
		if len(primary) > limit {
			primary = primary[:limit]
		}
		rendered = primary
	} else {
		rendered = primary
		if trailing != nil {
			rendered = append(rendered, *trailing)
		}
	}

	if len(rendered) == 0 {
		return ""
	}

	cmdWidth := 0
	for _, s := range rendered {
		if n := len(s.Command); n > cmdWidth {
			cmdWidth = n
		}
	}

	var b strings.Builder
	for i, s := range rendered {
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
