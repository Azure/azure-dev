// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"time"
)

// CheckFunc is the signature every check satisfies. Checks are invoked
// sequentially by the Runner; each receives the immutable Options and the
// list of Results produced by prior checks (for downstream checks that
// want to short-circuit when an upstream dependency failed).
//
// The prior slice must be treated as read-only — the Runner passes its
// live append target, and mutating elements would silently corrupt the
// final Report. Read-only inspection of prior[i].Status, .ID, .Name is
// the supported use case.
//
// Returning StatusSkip in response to a missing precondition is preferred
// over StatusFail — the user should not see a "fail" cascade when the
// root cause is a single upstream issue.
type CheckFunc func(ctx context.Context, opts Options, prior []Result) Result

// Check pairs a stable identifier with its execution function. ID is the
// value stamped onto the produced Result (the function itself does not
// populate ID — the Runner does this so the canonical IDs are owned in
// one place and cannot drift from the design's pinned table). Remote
// indicates whether the check requires network access; the Runner skips
// remote checks when Options.LocalOnly is true.
type Check struct {
	ID     string
	Name   string
	Remote bool
	Fn     CheckFunc
}

// Runner executes a list of checks against an azd project and produces a
// Report. The Runner itself is transport-free: it does not touch the
// filesystem, gRPC, or stdout. Checks bring those dependencies via their
// own closures (see checks_local.go).
type Runner struct {
	Checks []Check
}

// Run invokes every configured check, in order, gathering results and
// aggregating the Summary. Cancellation via ctx is honored on a
// best-effort basis: the loop checks ctx.Err() between checks and
// short-circuits the remainder as Skipped with a "cancelled" message.
//
// Run never returns an error — failures are encoded into the per-check
// Status and Message, so callers always receive a complete Report. This
// keeps the JSON envelope shape stable and lets the formatter render
// partial results when the runner is interrupted mid-flight.
func (r *Runner) Run(ctx context.Context, opts Options) Report {
	report := Report{
		SchemaVersion: CurrentSchemaVersion,
		Redacted:      !opts.Unredacted,
		Checks:        make([]Result, 0, len(r.Checks)),
	}

	for _, check := range r.Checks {
		if err := ctx.Err(); err != nil {
			report.Checks = append(report.Checks, Result{
				ID:      check.ID,
				Name:    check.Name,
				Status:  StatusSkip,
				Message: "cancelled",
			})
			continue
		}

		if opts.LocalOnly && check.Remote {
			report.Checks = append(report.Checks, Result{
				ID:      check.ID,
				Name:    check.Name,
				Status:  StatusSkip,
				Message: "remote check excluded by --local-only",
			})
			continue
		}

		// Defensive default for a malformed Check entry — fail loud rather
		// than silently dropping the check from the report.
		if check.Fn == nil {
			report.Checks = append(report.Checks, Result{
				ID:      check.ID,
				Name:    check.Name,
				Status:  StatusFail,
				Message: "internal error: check function is nil",
			})
			continue
		}

		start := time.Now()
		result := check.Fn(ctx, opts, report.Checks)
		result.DurationMs = time.Since(start).Milliseconds()
		// Pin the ID + Name at the runner — the design's table is the
		// source of truth, and individual check functions should not be
		// able to drift from it.
		result.ID = check.ID
		result.Name = check.Name
		// Normalize the returned Status. The Status type is `type Status
		// string`, which is *not* a closed enum at the Go type system
		// level: a check could return Status("passed") (a typo) or any
		// other string. We coerce any non-canonical value (including
		// empty) to StatusFail so the report is honest about the
		// internal error and the failed check is visible in summary +
		// exit code, rather than silently dropped.
		switch result.Status {
		case StatusPass, StatusWarn, StatusFail, StatusSkip:
			// canonical — keep as-is
		case "":
			result.Status = StatusFail
			if result.Message == "" {
				result.Message = "internal error: check returned empty status"
			}
		default:
			invalid := string(result.Status)
			result.Status = StatusFail
			if result.Message == "" {
				result.Message = "internal error: check returned invalid status: " + invalid
			}
		}
		report.Checks = append(report.Checks, result)

		if check.Remote {
			report.Remote = true
		}
	}

	report.Summary = summarize(report.Checks)
	return report
}

// summarize counts results by status. Unknown statuses are not expected
// here — the runner normalizes any non-canonical status to StatusFail
// before append — but we still ignore them defensively to keep the
// function robust against an externally-constructed Report.
func summarize(checks []Result) Summary {
	var s Summary
	for _, c := range checks {
		switch c.Status {
		case StatusPass:
			s.Pass++
		case StatusWarn:
			s.Warn++
		case StatusFail:
			s.Fail++
		case StatusSkip:
			s.Skip++
		}
	}
	return s
}

// ExitCode maps a Report onto the process exit code the doctor command
// should yield:
//
//   - 0 — at least one Pass and no Fail (Warn does not raise the exit
//     code; Skip does not lower the exit code below 0).
//   - 1 — any Fail (precedence over everything else).
//   - 2 — no useful diagnostic completed (empty report, all-skip,
//     warn-only, or any combination of skip + warn without a single
//     pass). The user needs to fix preconditions and re-run.
//
// A report with zero checks (which Run never produces but a caller might
// synthesize) yields exit code 2 — the "nothing ran" semantics match the
// all-skip case from the user's perspective.
func ExitCode(report Report) int {
	if report.Summary.Fail > 0 {
		return 1
	}
	if report.Summary.Pass == 0 {
		return 2
	}
	return 0
}
