// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

// NewRemoteChecks returns the canonical sequence of remote (network-
// dependent) doctor checks in execution order. The slice today
// contains four entries — `remote.auth` (P5.1 C11),
// `remote.foundry-endpoint` (P5.1 C12), `remote.rbac` (P5.1 C16),
// and `remote.agent-status` (P5.1 C17) — and is wired through
// `--local-only`, the runner's `Remote: true` gating
// (runner.go:74-82), and `report.Remote` (set when any executed
// check is Remote) so that downstream commits can append individual
// checks without touching the doctor command's Cobra wiring.
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
//   - `local.project-endpoint-set` for FOUNDRY_PROJECT_ENDPOINT
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
	//   - C12 (landed): foundry project endpoint reachability
	//     (`remote.foundry-endpoint`)
	//   - C16 (landed): developer RBAC on the Foundry project
	//     (`remote.rbac`)
	//   - C17 (landed): per-service agent version status
	//     (`remote.agent-status`)
	//   - C12 (landed): per-agent managed-identity role
	//     listing across project/account/RG scopes
	//     (`remote.agent-identity-roles`)
	//   - C15 (landed): connections exist on the
	//     Foundry project (`remote.connections`)
	//
	// Note: a `remote.model-deployments` check (C13) was removed
	// after release because its comparison was incorrect — a
	// config-declared resource name is a logical alias used to bind
	// `{{token}}` placeholders in `agent.yaml`, not a Foundry
	// deployment name. The redesign needs to read the resolved
	// deployment name from `agent.yaml`'s `environment_variables`
	// (or the azd env) instead. See nextstep/resources.go for the
	// populated `state.ModelRefs` slice that the new check can reuse.
	//
	// Ordering matters for skip-cascade: each entry reads `prior
	// []Result` produced by every check earlier in the combined
	// local-then-remote sequence. Append checks in the order their
	// preconditions resolve so a downstream check can short-circuit
	// when an upstream check fails.
	return []Check{
		newCheckAuth(deps),
		newCheckFoundryEndpoint(deps),
		newCheckRBAC(deps),
		newCheckAgentStatus(deps),
		newCheckAgentIdentityRoles(deps),
		newCheckConnections(deps),
	}
}
