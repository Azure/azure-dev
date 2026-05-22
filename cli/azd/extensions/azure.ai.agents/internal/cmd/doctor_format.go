// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"azureaiagent/internal/cmd/doctor"
	"azureaiagent/internal/cmd/nextstep"
)

// Category labels emitted as section headers in the doctor report. They are
// derived from a check's stable ID prefix; see categoryForCheck.
const (
	categoryLocal  = "Local"
	categoryAuth   = "Authentication"
	categoryRemote = "Remote"
)

// doctorRenderState streams a doctor report to a writer. It tracks the
// previously rendered category so section headers (Local / Authentication /
// Remote) are emitted exactly once, in stream order. The same state object is
// shared by both the streaming (`runAndRenderDoctorText`) and buffered
// (`printDoctorReportText`) paths so their outputs match byte-for-byte.
type doctorRenderState struct {
	w       io.Writer
	debug   bool
	headed  bool
	lastCat string
}

// newDoctorRenderer creates a renderer for one doctor run. `debug=false`
// produces the default concise output; `debug=true` produces the verbose
// per-check Message/Suggestion/Links block. The persistent root `--debug`
// flag controls this.
func newDoctorRenderer(w io.Writer, debug bool) *doctorRenderState {
	return &doctorRenderState{w: w, debug: debug}
}

// writeHeader emits the report title; safe to call only once.
func (r *doctorRenderState) writeHeader() error {
	if r.headed {
		return nil
	}
	r.headed = true
	_, err := fmt.Fprintln(r.w, "azd ai agent doctor")
	return err
}

// writeCheck emits a single check result. It emits a section header on the
// first check of a new category. Detail rendering depends on r.debug.
func (r *doctorRenderState) writeCheck(c doctor.Result) error {
	cat := categoryForCheck(c.ID)
	if cat != r.lastCat {
		if _, err := fmt.Fprintln(r.w); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(r.w, cat); err != nil {
			return err
		}
		r.lastCat = cat
	}

	if r.debug {
		return r.writeCheckVerbose(c)
	}
	return r.writeCheckConcise(c)
}

// writeFooter emits the summary line and, when applicable, an actionable
// "To fix" block (on failure) or a "Next:" block (on all-green). `showNext`
// gates the all-green block to TTY callers; the failure block always renders.
func (r *doctorRenderState) writeFooter(
	report doctor.Report,
	trailing []nextstep.Suggestion,
	showNext bool,
) error {
	if _, err := fmt.Fprintln(r.w); err != nil {
		return err
	}
	if err := writeSummaryLine(r.w, report.Summary); err != nil {
		return err
	}

	if report.Summary.Fail > 0 {
		return writeToFixBlock(r.w, report)
	}
	if showNext {
		return nextstep.PrintAllNext(r.w, trailing)
	}
	return nil
}

// printDoctorReportText is the buffered (non-streaming) entry point. It is
// used by tests and any caller that has a fully assembled Report. The flow
// matches the streaming path exactly so test assertions on the streaming
// path (TestPrintDoctorReportText_StreamingPiecesMatchBufferedReport) hold.
func printDoctorReportText(
	w io.Writer,
	report doctor.Report,
	trailing []nextstep.Suggestion,
	showNext bool,
	debug bool,
) error {
	r := newDoctorRenderer(w, debug)
	if err := r.writeHeader(); err != nil {
		return err
	}
	for _, c := range report.Checks {
		if err := r.writeCheck(c); err != nil {
			return err
		}
	}
	return r.writeFooter(report, trailing, showNext)
}

// writeCheckConcise emits the default-mode line for a check:
//
//	(✓) <Name>
//	(x) <Name>
//	    <first line of Message>
//	    fix: <first line of Suggestion>
//
// PASS suppresses Message/Suggestion/Links to keep the report scannable.
// FAIL/WARN keeps a one-line Message + Suggestion. SKIP inlines the reason
// after "-- skipped" when the Message starts with "skipped: ".
func (r *doctorRenderState) writeCheckConcise(c doctor.Result) error {
	glyph := statusGlyph(c.Status)
	switch c.Status {
	case doctor.StatusPass:
		_, err := fmt.Fprintf(r.w, "   %s %s\n", glyph, c.Name)
		return err
	case doctor.StatusSkip:
		reason := strings.TrimPrefix(c.Message, "skipped: ")
		reason = firstLine(reason)
		if reason == "" {
			_, err := fmt.Fprintf(r.w, "   %s %s -- skipped\n", glyph, c.Name)
			return err
		}
		_, err := fmt.Fprintf(r.w, "   %s %s -- skipped (%s)\n", glyph, c.Name, reason)
		return err
	default:
		// FAIL / WARN / INFO / UNKN
		if _, err := fmt.Fprintf(r.w, "   %s %s\n", glyph, c.Name); err != nil {
			return err
		}
		if msg := firstLine(c.Message); msg != "" {
			if _, err := fmt.Fprintf(r.w, "       %s\n", capitalize(msg)); err != nil {
				return err
			}
		}
		if sug := firstLine(c.Suggestion); sug != "" {
			if _, err := fmt.Fprintf(r.w, "       fix: %s\n", capitalize(sug)); err != nil {
				return err
			}
		}
		return nil
	}
}

// writeCheckVerbose emits the --debug mode line for a check; preserves the
// full Message, Suggestion, and Links contents and capitalizes the first
// letter of each so the output matches the user-visible feedback in #8198.
func (r *doctorRenderState) writeCheckVerbose(c doctor.Result) error {
	glyph := statusGlyph(c.Status)
	if _, err := fmt.Fprintf(r.w, "   %s %s\n", glyph, c.Name); err != nil {
		return err
	}
	if c.Message != "" {
		if err := writeIndentedBlock(r.w, "       ", capitalize(c.Message)); err != nil {
			return err
		}
	}
	if c.Suggestion != "" {
		if err := writeIndentedBlock(r.w, "       fix: ", capitalize(c.Suggestion)); err != nil {
			return err
		}
	}
	for _, link := range c.Links {
		if _, err := fmt.Fprintf(r.w, "       %s\n", link); err != nil {
			return err
		}
	}
	return nil
}

// writeIndentedBlock writes a multi-line block with the given prefix on the
// first line and a same-width whitespace prefix on continuation lines.
func writeIndentedBlock(w io.Writer, prefix, body string) error {
	lines := strings.Split(body, "\n")
	if len(lines) == 0 {
		return nil
	}
	contPrefix := strings.Repeat(" ", utf8.RuneCountInString(prefix))
	for i, line := range lines {
		if i == 0 {
			if _, err := fmt.Fprintf(w, "%s%s\n", prefix, line); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(w, "%s%s\n", contPrefix, line); err != nil {
			return err
		}
	}
	return nil
}

// writeSummaryLine emits the aggregate counts in the new "X passed, Y
// failed, Z skipped" format. Warn and Info segments are appended only when
// non-zero so the line stays uncluttered for the common case.
func writeSummaryLine(w io.Writer, s doctor.Summary) error {
	if s.Pass == 0 && s.Warn == 0 && s.Fail == 0 && s.Skip == 0 && s.Info == 0 {
		_, err := fmt.Fprintln(w, "No checks executed")
		return err
	}
	parts := []string{
		fmt.Sprintf("%d passed", s.Pass),
		fmt.Sprintf("%d failed", s.Fail),
		fmt.Sprintf("%d skipped", s.Skip),
	}
	if s.Warn > 0 {
		parts = append(parts, fmt.Sprintf("%d warned", s.Warn))
	}
	if s.Info > 0 {
		parts = append(parts, fmt.Sprintf("%d info", s.Info))
	}
	_, err := fmt.Fprintln(w, strings.Join(parts, ", "))
	return err
}

// writeToFixBlock emits the "To fix" footer on failed reports. When at least
// one failed check maps to a canonical remediation (`remediationForCheckID`),
// the footer is a numbered, deduplicated command list in execution order
// (auth → provision → deploy). When all failures are unmapped (or there are
// any unmapped failures alongside mapped ones), the footer also defers to
// the per-check `fix:` notes rendered in the body for full coverage. The
// re-run instruction always closes the block so the user knows how to
// re-validate.
func writeToFixBlock(w io.Writer, report doctor.Report) error {
	if report.Summary.Fail == 0 {
		return nil
	}
	actions := orderedRemediations(report)
	unmapped := hasUnmappedFailure(report)
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	switch {
	case len(actions) > 0:
		if _, err := fmt.Fprintln(w, "To fix, run these commands in order:"); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		cmdWidth := 0
		for _, a := range actions {
			if n := len(a.command); n > cmdWidth {
				cmdWidth = n
			}
		}
		for i, a := range actions {
			pad := strings.Repeat(" ", cmdWidth-len(a.command))
			if _, err := fmt.Fprintf(w, "  %d. %s%s  -- %s\n", i+1, a.command, pad, a.desc); err != nil {
				return err
			}
		}
		if unmapped {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(
				w, "Also review the fix: notes above for any remaining failed checks.",
			); err != nil {
				return err
			}
		}
	default:
		if _, err := fmt.Fprintln(w, "To fix:"); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(
			w, "  Review the fix: notes above for each failed check.",
		); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w, "Then re-run `azd ai agent doctor` to verify.")
	return err
}

// hasUnmappedFailure reports whether any failed check lacks a canonical
// remediation entry in `remediationForCheckID`. Used by `writeToFixBlock`
// to know when to append the "also review fix: notes" pointer.
func hasUnmappedFailure(report doctor.Report) bool {
	for _, c := range report.Checks {
		if c.Status != doctor.StatusFail {
			continue
		}
		if _, ok := remediationForCheckID(c.ID); !ok {
			return true
		}
	}
	return false
}

// remediation is one row in the "To fix" block.
type remediation struct {
	command string
	desc    string
	order   int
}

// orderedRemediations maps failed check IDs onto an ordered, deduplicated
// list of remediations. The order field expresses the canonical execution
// sequence (login → provision → deploy → init).
func orderedRemediations(report doctor.Report) []remediation {
	seen := map[string]remediation{}
	for _, c := range report.Checks {
		if c.Status != doctor.StatusFail {
			continue
		}
		r, ok := remediationForCheckID(c.ID)
		if !ok {
			continue
		}
		// First failed check wins so deterministic remediation text is preserved.
		if _, exists := seen[r.command]; !exists {
			seen[r.command] = r
		}
	}
	out := make([]remediation, 0, len(seen))
	for _, r := range seen {
		out = append(out, r)
	}
	// stable sort by canonical order
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].order > out[j].order; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// remediationForCheckID is the canonical mapping from failed check ID to
// remediation command. Unknown IDs return ok=false; the per-check fix: text
// is the source of truth in that case.
func remediationForCheckID(id string) (remediation, bool) {
	switch id {
	case "remote.auth":
		return remediation{command: "azd auth login", desc: "sign in to Azure", order: 10}, true
	case "remote.foundry-endpoint",
		"remote.model-deployments",
		"remote.connections",
		"remote.agent-identity-roles":
		return remediation{
			command: "azd provision",
			desc:    "create the missing Foundry resources",
			order:   20,
		}, true
	case "remote.agent-status":
		return remediation{
			command: "azd deploy",
			desc:    "deploy the agent(s)",
			order:   30,
		}, true
	case "local.azure-yaml", "local.agent-service-detected":
		return remediation{
			command: "azd ai agent init",
			desc:    "scaffold or refresh the agent project",
			order:   5,
		}, true
	case "local.environment-selected":
		return remediation{
			command: "azd env new",
			desc:    "create or select an azd environment",
			order:   1,
		}, true
	}
	return remediation{}, false
}

// categoryForCheck derives the section label from a check's stable ID. The
// `remote.auth` check is split into its own Authentication section because
// it is the only credential-related probe and reads more naturally on its
// own line in the rendered report.
func categoryForCheck(id string) string {
	switch {
	case id == "remote.auth":
		return categoryAuth
	case strings.HasPrefix(id, "remote."):
		return categoryRemote
	case strings.HasPrefix(id, "local."):
		return categoryLocal
	default:
		return categoryRemote
	}
}

// statusGlyph returns the bracketed indicator emitted before each check
// name. The format intentionally mirrors common CLI conventions (e.g.,
// `(✓)`, `(x)`) for low-effort scanning.
func statusGlyph(s doctor.Status) string {
	switch s {
	case doctor.StatusPass:
		return "(✓)"
	case doctor.StatusWarn:
		return "(!)"
	case doctor.StatusFail:
		return "(x)"
	case doctor.StatusSkip:
		return "(-)"
	case doctor.StatusInfo:
		return "(ⓘ)"
	default:
		return "(?)"
	}
}

// statusGlyphAndLabel is retained for tests that pin the per-status glyph
// contract. New rendering code uses statusGlyph directly because the new
// format does not include a fixed-width label segment.
func statusGlyphAndLabel(s doctor.Status) (string, string) {
	switch s {
	case doctor.StatusPass:
		return "(✓)", "PASS"
	case doctor.StatusWarn:
		return "(!)", "WARN"
	case doctor.StatusFail:
		return "(x)", "FAIL"
	case doctor.StatusSkip:
		return "(-)", "SKIP"
	case doctor.StatusInfo:
		return "(ⓘ)", "INFO"
	default:
		return "(?)", "UNKN"
	}
}

// firstLine returns the first line of s with surrounding whitespace
// trimmed. It is the concise-mode mechanism for collapsing a multi-line
// Message or Suggestion to a single scannable line.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

// capitalize uppercases the first rune of s while leaving the remainder
// untouched. It is a no-op when s begins with a non-letter (e.g., a number,
// an env var like FOUNDRY_PROJECT_ENDPOINT, or a backtick-quoted token),
// when s is already capitalized, or when s begins with a known brand-name
// prefix that is conventionally lowercase ("azd", "azure.yaml", "agent.yaml",
// "skipped:", "cancelled"). The render-layer rule mirrors the convention in
// the user-visible mock in PR #8198 review 4331086010.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	if !unicode.IsLetter(r) || unicode.IsUpper(r) {
		return s
	}
	lower := strings.ToLower(s)
	for _, prefix := range noCapitalizePrefixes {
		if strings.HasPrefix(lower, prefix) {
			return s
		}
	}
	return string(unicode.ToUpper(r)) + s[size:]
}

// noCapitalizePrefixes lists lowercase-leading prefixes that must remain
// lowercase in the rendered report. Each prefix is matched case-insensitively
// against the start of the string. Add new entries only for brand names,
// command names, or token prefixes that read more naturally lowercase in
// terminal output (e.g., `azd`, `azure.yaml`).
var noCapitalizePrefixes = []string{
	"azd ",
	"azd.",
	"azure.yaml",
	"agent.yaml",
	"agent.manifest.yaml",
	"skipped:",
	"skipped ",
}
