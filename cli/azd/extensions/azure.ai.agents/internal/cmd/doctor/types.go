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

// CurrentSchemaVersion is the version stamped onto the JSON envelope. Bump
// when the JSON shape changes in a non-additive way; additive changes
// (new optional fields, new status values that consumers can ignore) do
// not require a bump. Consumers should treat unknown statuses as "pass"
// for the purposes of summary aggregation only when this version equals
// the one they were built against.
const CurrentSchemaVersion = "1"

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
// ID is a stable identifier (the design pins these to "1".."12"). Name is
// a short human-readable title for the text formatter; Message is the
// one-line summary that always renders. Details and Suggestion are
// optional — Details is a structured map for machine consumers (the JSON
// formatter emits it as an object; the text formatter renders each
// key-value pair on an indented line), Suggestion is a single actionable
// command or instruction (the text formatter renders it on its own line
// prefixed with "→ ").
type Result struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Status     Status         `json:"status"`
	Message    string         `json:"message,omitempty"`
	Details    map[string]any `json:"details,omitempty"`
	Suggestion string         `json:"suggestion,omitempty"`
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
type Report struct {
	SchemaVersion string   `json:"schemaVersion"`
	Remote        bool     `json:"remote"`
	Redacted      bool     `json:"redacted"`
	Checks        []Result `json:"checks"`
	Summary       Summary  `json:"summary"`
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
