// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"io"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

const (
	// header labels the guidance block. It sits on its own line; the
	// suggestions are stacked beneath it, indented by bodyIndent.
	header = "Next:"
	// bodyIndent indents each command and description line under the
	// header, creating a clear visual hierarchy ("Next:" at the margin,
	// the actionable lines stepped in beneath it).
	bodyIndent = "  "
	// maxRendered caps PrintNext at this many suggestions (one primary
	// plus one optional Trailing footer). PrintAllNext and
	// FormatNextForNote are uncapped.
	maxRendered = 2
)

// PrintNext writes a "Next:" guidance block to w. Suggestions are sorted
// ascending by Priority (stable; ties preserve input order) and then
// truncated to a primary plus an optional Trailing suggestion. Empty
// input produces no output and no write.
//
// PrintNext does not inspect TTY state or output-format flags — those
// decisions live at the call site so the same renderer can serve both
// interactive stdout writes and string capture for tests / JSON envelopes.
// Command highlighting is applied by the renderer via output.WithHighLightFormat,
// which self-gates on color settings (NO_COLOR / non-TTY yield plain text).
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
// interleaved with command output).
//
// The returned string begins with a newline and has no trailing newline.
// Core azd's artifact renderer appends the note via "\n%s  %s" with the
// caller indent applied to the first line only (see
// cli/azd/pkg/project/artifact.go). The leading newline turns that first,
// indent-only line into a blank separator, after which the "Next:" header
// and its stacked, bodyIndent-indented suggestions render beneath the
// endpoint. The deploy path renders artifacts at the zero indent, so the
// header lands in the same column as the endpoint bullet and each
// suggestion steps in by bodyIndent.
//
// Empty input returns an empty string.
func FormatNextForNote(suggestions []Suggestion) string {
	body := renderRows(suggestions, 0)
	if body == "" {
		return ""
	}
	return "\n" + strings.TrimSuffix(body, "\n")
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

// renderRows returns the formatted "Next:" body: a header line followed
// by each suggestion rendered as an indented command line, its indented
// description line, and a blank line separating consecutive suggestions.
// The body has no leading blank line and ends with "\n". limit caps the
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

	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	for i, s := range rendered {
		if i > 0 {
			// Blank line between suggestions so each command/description
			// pair reads as a distinct step.
			b.WriteByte('\n')
		}
		b.WriteString(bodyIndent)
		b.WriteString(highlightCommand(s.Command))
		b.WriteByte('\n')
		if s.Description != "" {
			b.WriteString(bodyIndent)
			b.WriteString(s.Description)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// highlightCommand returns cmd wrapped in the highlight (blue) color when
// it is a runnable azd command (prefix "azd "), and cmd unchanged
// otherwise. Non-command suggestions — "see <path>/README.md" pointers and
// "edit agent.yaml: ..." instructions — stay plain.
//
// output.WithHighLightFormat gates on color.NoColor. In this extension that
// flag is driven by the FORCE_COLOR env var azd core sets when core itself
// is in color mode (interactive TTY, NO_COLOR unset); for piped, redirected,
// or NO_COLOR runs core omits it, so color.NoColor is true and commands stay
// plain. This keeps highlighting aligned with the host's color state without
// the renderer inspecting TTY/output flags directly.
//
// Known limitation: core sets FORCE_COLOR from its own TTY state without
// accounting for --output json, so a block rendered into an artifact note
// (see FormatNextForNote) during `--output json` from a color TTY can carry
// ANSI into the JSON. Closing that gap requires a core change (skip
// FORCE_COLOR for JSON output); the common scripted/piped JSON case is
// non-TTY, so color.NoColor is already true there and the note stays plain.
func highlightCommand(cmd string) string {
	if strings.HasPrefix(cmd, "azd ") {
		return output.WithHighLightFormat("%s", cmd)
	}
	return cmd
}
