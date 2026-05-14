// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"azureaiagent/internal/cmd/doctor"
	"azureaiagent/internal/cmd/nextstep"
)

// renderDoctorReport routes a Report to the text or JSON formatter
// based on the `--output` flag. trailing is the optional Next: block
// computed by the resolver — used only by the text formatter when
// stdout is a TTY (the JSON envelope deliberately excludes the human
// next-step block per the design spec).
func renderDoctorReport(
	w io.Writer,
	output string,
	report doctor.Report,
	trailing []nextstep.Suggestion,
) error {
	switch output {
	case "json":
		return printDoctorReportJSON(w, report)
	default:
		showNext := len(trailing) > 0 && writerIsTerminal(w)
		return printDoctorReportText(w, report, trailing, showNext)
	}
}

// writerIsTerminal reports whether w is the OS stdout AND that fd is
// attached to an interactive terminal. The Next: block is suppressed
// for non-stdout writers (test capture, file redirection, pipes) so
// scripted consumers of the text output never see surprise trailing
// lines. Callers that want the block unconditionally (tests) construct
// the rendered string directly via printDoctorReportText with
// showNext=true.
func writerIsTerminal(w io.Writer) bool {
	if w == os.Stdout {
		return isTerminal(os.Stdout.Fd())
	}
	return false
}

// printDoctorReportJSON emits the structured envelope defined in the
// design spec (`docs/design/azd-ai-agent-nextsteps.md`, "Exit codes &
// JSON output"). The envelope is `{schemaVersion, remote, redacted,
// checks: [...]}` and is stable across additive changes (new check
// IDs, new optional fields). The human Next: block is not part of the
// envelope — that is a deliberate output-discipline contract.
//
// Trailing newline is included so the output is well-formed when
// followed by other lines (test capture) and so terminals do not
// merge the closing brace with the next prompt.
func printDoctorReportJSON(w io.Writer, report doctor.Report) error {
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal doctor report to JSON: %w", err)
	}
	_, err = fmt.Fprintln(w, string(encoded))
	return err
}

// printDoctorReportText renders the human-readable doctor report. The
// shape mirrors the design spec at "Doctor output shape":
//
//	azd ai agent doctor
//	  ✓ PASS  <check name>
//	          <one-line detail>
//	  ✗ FAIL  <check name>
//	          <one-line detail>
//	          fix:  <command or instruction>
//
//	Next:  <resolved next-step block>
//
// Glyph + label combination provides both visual signal (glyph for
// quick scan) and accessibility (label for screen readers / non-UTF8
// terminals). All four canonical statuses get a fixed-width 4-char
// label so check names align in a column.
//
// Summary line is appended after the per-check block.
//
// The trailing Next: block is rendered only when showNext is true.
// nextstep.PrintAllNext owns the leading blank-line separator (see
// nextstep/format.go renderBlock), so this function does not pre-emit
// one. PrintAllNext (not PrintNext) is used because doctor surfaces
// the same multi-category fix-up list as `azd ai agent init` — every
// line is a required action, and silently dropping any of them would
// hide work the user still has to do.
func printDoctorReportText(
	w io.Writer,
	report doctor.Report,
	trailing []nextstep.Suggestion,
	showNext bool,
) error {
	if _, err := fmt.Fprintln(w, "azd ai agent doctor"); err != nil {
		return err
	}

	for _, c := range report.Checks {
		if err := writeCheckLines(w, c); err != nil {
			return err
		}
	}

	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if err := writeSummaryLine(w, report.Summary); err != nil {
		return err
	}

	if showNext {
		if err := nextstep.PrintAllNext(w, trailing); err != nil {
			return err
		}
	}

	return nil
}

// writeCheckLines emits one Result as a status header line plus
// indented continuation lines for message, suggestion, and any links.
// Empty fields are silently elided — the formatter is responsible for
// not rendering a "fix:" prefix on top of an empty Suggestion.
//
// Indentation is hardcoded to 2 + 8 spaces (header indent + label
// width including trailing gap) so continuation text aligns under
// the check name column.
func writeCheckLines(w io.Writer, c doctor.Result) error {
	glyph, label := statusGlyphAndLabel(c.Status)
	if _, err := fmt.Fprintf(w, "  %s %s  %s\n", glyph, label, c.Name); err != nil {
		return err
	}
	if c.Message != "" {
		if _, err := fmt.Fprintf(w, "          %s\n", c.Message); err != nil {
			return err
		}
	}
	if c.Suggestion != "" {
		if _, err := fmt.Fprintf(w, "          fix:  %s\n", c.Suggestion); err != nil {
			return err
		}
	}
	for _, link := range c.Links {
		if _, err := fmt.Fprintf(w, "          %s\n", link); err != nil {
			return err
		}
	}
	return nil
}

// statusGlyphAndLabel returns the glyph + 4-char label for a Status.
// Unknown statuses (which the runner normalizes to StatusFail before
// reaching the formatter) get a "?" glyph and "UNKN" label so the
// formatter never silently drops a check.
func statusGlyphAndLabel(s doctor.Status) (string, string) {
	switch s {
	case doctor.StatusPass:
		return "✓", "PASS"
	case doctor.StatusWarn:
		return "!", "WARN"
	case doctor.StatusFail:
		return "✗", "FAIL"
	case doctor.StatusSkip:
		return "-", "SKIP"
	case doctor.StatusInfo:
		// ⓘ (U+24D8) carries strong "informational, no action" semantic in
		// monospace terminal output and matches the design's example
		// (azd-ai-agent-doctor-remote-checks.md:209). The 4-char label
		// keeps column alignment with the four pre-existing statuses.
		return "ⓘ", "INFO"
	default:
		return "?", "UNKN"
	}
}

// writeSummaryLine emits the aggregate count of results. The format is
// "Summary: N passed, N failed, N skipped, N warned" with categories
// elided when their count is zero (except the very common "0 failed
// 0 warned" combo, which we keep visible so users see the all-clean
// picture at a glance). An optional ", N info" suffix is appended
// only when at least one check produced an informational result —
// this keeps the line concise for the common case (zero-info checks)
// and preserves backwards-compat with consumers asserting the
// four-category form.
//
// When every category is zero (an empty Report — runtime should never
// produce this but a caller might synthesize it) we render "Summary:
// no checks executed" so the output is not just "Summary: ".
func writeSummaryLine(w io.Writer, s doctor.Summary) error {
	if s.Pass == 0 && s.Warn == 0 && s.Fail == 0 && s.Skip == 0 && s.Info == 0 {
		_, err := fmt.Fprintln(w, "Summary: no checks executed")
		return err
	}
	if s.Info > 0 {
		_, err := fmt.Fprintf(
			w,
			"Summary: %d passed, %d failed, %d skipped, %d warned, %d info\n",
			s.Pass, s.Fail, s.Skip, s.Warn, s.Info,
		)
		return err
	}
	_, err := fmt.Fprintf(
		w,
		"Summary: %d passed, %d failed, %d skipped, %d warned\n",
		s.Pass, s.Fail, s.Skip, s.Warn,
	)
	return err
}
