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
// The caller passes colorize to opt into highlighting of `azd` commands;
// pass true only when w is an interactive terminal (highlighting still
// self-gates on NO_COLOR via the output package). Pass false for non-TTY
// writers and any path whose bytes may be captured into machine-readable
// output.
//
// Use PrintAllNext when the resolver produces multiple REQUIRED follow-up
// actions (init / doctor fix-ups) where silently dropping any of them
// would mislead the user.
func PrintNext(w io.Writer, suggestions []Suggestion, colorize bool) error {
	block := renderBlock(suggestions, maxRendered, colorize)
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
// (leading blank line + trailing newline) matches PrintNext. colorize
// behaves as documented on PrintNext. Empty input is a no-op.
func PrintAllNext(w io.Writer, suggestions []Suggestion, colorize bool) error {
	block := renderBlock(suggestions, 0, colorize)
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
// The output is always plain (uncolored). The note is data returned to
// the azd host over gRPC, which both renders it to the terminal AND
// serializes it into `azd deploy --output json`. The service-target
// Deploy path has no output-format signal, so embedding ANSI escape
// codes here would leak them into JSON output on color-enabled
// terminals. Highlighting of `azd` commands is therefore reserved for
// the direct-to-terminal PrintNext / PrintAllNext paths, which receive
// an explicit colorize signal from their call sites.
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
	body := renderRows(suggestions, 0, false)
	if body == "" {
		return ""
	}
	return "\n" + strings.TrimSuffix(body, "\n")
}

// renderBlock returns the formatted "Next:" block (with a leading blank
// line and trailing newline) or an empty string when there is nothing to
// render. limit is forwarded to renderRows: a positive value caps the
// block at that many visible lines (PrintNext default), while limit <= 0
// renders every suggestion (PrintAllNext). colorize is forwarded to the
// command renderer.
func renderBlock(suggestions []Suggestion, limit int, colorize bool) string {
	body := renderRows(suggestions, limit, colorize)
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
// suggestion. colorize is forwarded to highlightCommand.
//
// Truncation is partitioned: at most one Suggestion.Trailing entry is
// reserved for the final visible slot, with remaining slots filled by
// primary (non-trailing) entries in ascending Priority order. The
// trailing reservation lets resolvers emit follow-up nudges (e.g., the
// post-action `azd deploy` line) without having those nudges silently
// dropped when primary suggestions outnumber the cap.
func renderRows(suggestions []Suggestion, limit int, colorize bool) string {
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
		b.WriteString(highlightCommand(s.Command, colorize))
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
// colorize is true and cmd is a runnable azd command (prefix "azd "). When
// colorize is false, or cmd is a non-command pointer ("see <path>/README.md"
// or "edit agent.yaml: ..."), cmd is returned unchanged.
//
// colorize is supplied by the caller because output.WithHighLightFormat
// only self-gates on color settings (NO_COLOR / non-TTY) — it does NOT
// know about azd's --output json mode. Callers whose bytes may be captured
// into machine-readable output (e.g. FormatNextForNote, whose result is
// embedded in an artifact note that azd may serialize to JSON) must pass
// false so ANSI escape codes never leak into that output.
func highlightCommand(cmd string, colorize bool) string {
	if colorize && strings.HasPrefix(cmd, "azd ") {
		return output.WithHighLightFormat("%s", cmd)
	}
	return cmd
}
