// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package doctor implements `azd ai agent doctor` — a diagnostics command
// that runs a sequence of local checks (and, in a follow-up phase, remote
// checks) over the current azd project and prints a structured report.
//
// The package is split along two seams:
//
//   - This file (types.go) declares the data model: Status, Result,
//     Report, and Options. These are the shapes the runner produces and
//     the formatters consume; both seams pin against them.
//   - runner.go declares the Runner — a transport-free engine that
//     iterates a check list, gathers results, and computes the exit code.
//
// Check implementations live in checks_local.go (and, in phase 5,
// checks_remote.go). The Cobra wiring and formatters live in the parent
// internal/cmd package so this package has no Cobra / IO dependencies and
// can be unit-tested without a process-level shim.
package doctor

// CurrentSchemaVersion is the version stamped onto the JSON envelope. The
// value matches the design spec (`docs/design/azd-ai-agent-nextsteps.md`,
// "Exit codes & JSON output" section). Bump on non-additive shape changes;
// additive changes (new optional fields, new status values) do not require
// a bump.
const CurrentSchemaVersion = "1.0"

// Status is the outcome of a single check. The set is closed; runners and
// formatters branch exhaustively on these four values.
type Status string

const (
	// StatusPass — the check succeeded; no follow-up needed.
	StatusPass Status = "pass"
	// StatusWarn — the check completed but flagged a soft issue the user
	// may want to address. Does NOT contribute to a non-zero exit code.
	StatusWarn Status = "warn"
	// StatusFail — the check completed and identified a blocker. Drives
	// exit code 1 (see ExitCode in runner.go).
	StatusFail Status = "fail"
	// StatusSkip — the check did not run (precondition unmet, --local-only
	// excluded it, or an upstream dependency failed). Does NOT contribute
	// to a non-zero exit code on its own; a report consisting entirely of
	// skips yields exit code 2.
	StatusSkip Status = "skip"
)

// Result captures the outcome of one check.
//
// ID is a stable namespaced identifier (`local.azure-yaml`,
// `remote.rbac`, etc.). Name is a short human-readable title; Message is
// the one-line summary that always renders. Suggestion is a single
// actionable command or instruction (the text formatter renders it after
// the message, indented). Links is an optional slice of URLs (TSG pages,
// learn.microsoft.com docs) that the formatter renders below the
// suggestion. DurationMs is populated by the Runner per check.
//
// JSON tags are extension-owned: the wire shape includes `links` and
// `durationMs` (matching the design spec at
// `docs/design/azd-ai-agent-nextsteps.md`) plus a `details` extension
// field (omitted from the design's example but required for Phase 5
// remote checks that surface structured payload — role lists, scope
// ARNs). `details` is `omitempty`, so consumers built against the
// design's schema ignore the extra field and remain compatible.
type Result struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Status     Status         `json:"status"`
	Message    string         `json:"message,omitempty"`
	Suggestion string         `json:"suggestion,omitempty"`
	Links      []string       `json:"links,omitempty"`
	DurationMs int64          `json:"durationMs,omitempty"`
	Details    map[string]any `json:"details,omitempty"`
}

// Summary is the aggregate count of results by status. Computed by the
// runner; consumers should not mutate it.
type Summary struct {
	Pass int `json:"pass"`
	Warn int `json:"warn"`
	Fail int `json:"fail"`
	Skip int `json:"skip"`
}

// Report is the full structured output of a doctor run. SchemaVersion is
// the contract version (see CurrentSchemaVersion). Remote is true when
// remote checks (phase 5) ran; false when --local-only or when no remote
// checks are wired. Redacted is the inverse of the --unredacted flag and
// indicates whether the formatter scrubbed identifiers in user-facing
// strings.
//
// Summary is computed by the Runner for ExitCode and the text formatter,
// but is excluded from the JSON envelope (consumers iterate Checks if they
// need totals). Excluding it keeps the wire shape aligned with the design
// spec.
type Report struct {
	SchemaVersion string   `json:"schemaVersion"`
	Remote        bool     `json:"remote"`
	Redacted      bool     `json:"redacted"`
	Checks        []Result `json:"checks"`
	Summary       Summary  `json:"-"`
}

// Options are the runtime flags that influence the runner. LocalOnly
// excludes any check whose Remote field is true (no-op in phase 4 — no
// remote checks are wired yet; the field is exposed early so the Cobra
// surface can be locked without churn when phase 5 lands). Unredacted
// inverts Redacted on the produced Report; it is also surfaced to checks
// that decide whether to include identifiers in their Message / Details
// strings.
type Options struct {
	LocalOnly  bool
	Unredacted bool
}
