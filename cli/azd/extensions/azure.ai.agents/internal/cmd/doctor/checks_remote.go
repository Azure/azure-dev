// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

// NewRemoteChecks returns the canonical sequence of remote (network-
// dependent) doctor checks in execution order. Today the slice is
// empty — the framework is wired through `--local-only`, the runner's
// `Remote: true` gating (runner.go:74-82), and `report.Remote` (set
// when any executed check is Remote) so that downstream commits (P5
// C11 / C12 / C16 / C17) can append individual checks without
// touching the doctor command's Cobra wiring.
//
// # Conventions for remote checks added in C11+
//
//   - Set Remote: true on the Check value. The runner uses this both
//     to skip the check under --local-only and to flip
//     report.Remote = true when the check runs (used by the JSON
//     envelope and the formatter's "remote checks were exercised"
//     decisions).
//   - Forward the `Dependencies` struct to each check's closure. C11+
//     checks that require auth credentials or a REST client should
//     add those fields to `Dependencies` (defined in checks_local.go)
//     and document them there. Tests inject fakes via the same fields
//     that production wiring populates from the Cobra surface.
//   - Skip-cascade against the local chain. Most remote checks
//     require at least:
//   - `local.grpc-extension` to have produced an AzdClient
//   - `local.azure-yaml` for the project root
//   - `local.environment-selected` for the active azd env name
//   - `local.project-endpoint-set` for AZURE_AI_PROJECT_ENDPOINT
//     Guard with one or more `priorBlocked(prior, "<id>")` calls and
//     return Result{Status: StatusSkip, Message: "..."}. Doing the
//     work inside the check (rather than in the runner) keeps the
//     skip-message specific to the inherited failure so users see a
//     pointed suggestion instead of a generic "upstream check failed".
//   - Honor ctx cancellation. Remote checks own a network round trip;
//     the runner only checks ctx.Err between checks, so a long-blocked
//     HTTP call would otherwise stall a Ctrl-C.
//   - When Unredacted is false (the default), elide raw principal IDs
//     / scope ARNs / UPNs from the Message. The full payload still
//     goes into Details for callers that opt in via --unredacted.
//
// # Ordering relative to local checks
//
// In `doctor.go:runDoctor`, remote checks are appended AFTER all
// local checks. This is deliberate: every remote check's skip-cascade
// reads `prior []Result`, and the local results must be available in
// that slice when the remote check runs. The runner's loop preserves
// the order of `Runner.Checks`, so appending remote-after-local is
// sufficient.
func NewRemoteChecks(deps Dependencies) []Check {
	// Phase 5 commits append entries here:
	//   - C11 (landed): auth probe (`remote.auth`)
	//   - C12 (planned): foundry project endpoint reachability
	//     (`remote.foundry-endpoint`)
	//   - C16 (planned): RBAC permissions (`remote.rbac`)
	//   - C17 (planned): agent status on backend (`remote.agent-status`)
	// Ordering matters for skip-cascade: each entry reads `prior
	// []Result` produced by every check earlier in the combined
	// local-then-remote sequence. Append checks in the order their
	// preconditions resolve so a downstream check can short-circuit
	// when an upstream check fails.
	return []Check{
		newCheckAuth(deps),
	}
}
