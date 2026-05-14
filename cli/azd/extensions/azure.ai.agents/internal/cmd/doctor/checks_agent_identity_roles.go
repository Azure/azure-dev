// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// agentIdentityProbeTimeout caps each per-agent principal-ID fetch.
// Matches the agent-status probe's timeout (6 s) because the same
// `GetAgentVersion` endpoint serves both — a project that's
// returning principal IDs within budget for check 11 will do the
// same here. Held shorter than the overall check budget so a single
// stalled agent doesn't drag the doctor past the design's per-check
// 6 s ceiling.
const agentIdentityProbeTimeout = 6 * time.Second

// agentIdentityConcurrency bounds the per-agent fan-out used to
// fetch principal IDs in parallel. Matches probeConcurrency from
// the agent-status check; same rate-limit reasoning applies (Foundry
// per-token rate cap on the agents endpoint).
const agentIdentityConcurrency = 4

// agentIdentityClass values bucket a single agent's per-scope role
// inventory into a coarse class the aggregate folder consumes.
// Ordering used by `agentIdentityClassRank` is encoded by the int
// values: higher = worse for the aggregate.
const (
	agentIdentityClassFine        = "fine"        // project + (account|RG) → pass condition met
	agentIdentityClassUnderscoped = "underscoped" // assignments somewhere but pass condition unmet
	agentIdentityClassEmpty       = "empty"       // zero assignments anywhere reachable
	agentIdentityClassUnknown     = "unknown"     // probe error — principal fetch failed
)

// agentIdentityClassRank gives a strict ordering so the aggregate
// classifier can pick the dominant class without ambiguity. Higher
// values win.
var agentIdentityClassRank = map[string]int{
	agentIdentityClassFine:        0,
	agentIdentityClassUnknown:     1,
	agentIdentityClassUnderscoped: 2,
	agentIdentityClassEmpty:       3,
}

// agentIdentityRoleEntry is the unit the per-agent classifier
// produces. Surfaced verbatim under `Details["agents"]` for JSON
// consumers; aggregate Message picks the worst-class agent's name
// for headline rendering.
type agentIdentityRoleEntry struct {
	AgentName    string   `json:"agentName"`
	AgentVersion string   `json:"agentVersion,omitempty"`
	PrincipalID  string   `json:"principalId,omitempty"`
	ProjectRoles []string `json:"projectRoles"`
	AccountRoles []string `json:"accountRoles"`
	RGRoles      []string `json:"resourceGroupRoles"`

	// Errors per scope. nil = success (including legitimate empty
	// list); non-nil = probe failure (the listing is unknown).
	ProjectErr string `json:"projectErr,omitempty"`
	AccountErr string `json:"accountErr,omitempty"`
	RGErr      string `json:"resourceGroupErr,omitempty"`

	Class  string `json:"class"`
	Detail string `json:"detail"`
}

// agentIdentityProbeResult is the outcome of a single
// GetAgentVersion call used purely to extract
// `instance_identity.principal_id`. Distinct from
// agentStatusProbeResult because callers care about a different
// field (PrincipalID vs Status); keeping the type narrow protects
// against future drift.
type agentIdentityProbeResult struct {
	PrincipalID string
	StatusCode  int
	Err         error
}

// newCheckAgentIdentityRoles produces Check
// `remote.agent-identity-roles`. For each agent in the prior
// `remote.agent-status` Details that was classified Active, the
// check fetches the agent's `instance_identity.principal_id` and
// lists role assignments at three scopes (project, account, RG).
// Per-agent classification:
//
//   - fine        — project ≥ 1 and (account ≥ 1 OR RG ≥ 1) →
//     contributes to an aggregate INFO. Pass condition per design.
//   - underscoped — assignments somewhere but pass condition unmet
//     (e.g., project only). Aggregate folds onto WARN.
//   - empty       — zero assignments anywhere reachable. Aggregate
//     folds onto FAIL.
//   - unknown     — probe error during principal fetch or listing.
//     Single-occurrence on its own is rendered as WARN; aggregated
//     with empty entries it does not lower the FAIL severity.
//
// Aggregate rules:
//
//   - All "fine"           → INFO (informational role listing).
//   - Any "empty"          → FAIL (zero-roles agent is the
//     smoking-gun for "every tool call 403s").
//   - Worst is "underscoped" → WARN.
//   - Worst is "unknown" → WARN (probe couldn't classify).
//
// Skip cascade:
//   - `remote.agent-status` must Pass (per the design, "for each
//     active agent found in check 11").
//   - `local.environment-selected`, `local.agent-service-detected`,
//     `remote.auth`, `remote.foundry-endpoint` — same precondition
//     chain as `remote.agent-status`; surface a single Skip rather
//     than re-validating each.
func newCheckAgentIdentityRoles(deps Dependencies) Check {
	apiVersion := deps.AgentAPIVersion
	return Check{
		ID:     "remote.agent-identity-roles",
		Name:   "Agent identity role assignments",
		Remote: true,
		Fn: func(ctx context.Context, opts Options, prior []Result) Result {
			if deps.AzdClient == nil {
				return Result{
					Status:  StatusSkip,
					Message: "skipped: azd extension not reachable.",
				}
			}
			if priorBlocked(prior, "local.environment-selected") {
				return Result{
					Status: StatusSkip,
					Message: "skipped: no azd environment is selected " +
						"(see check `local.environment-selected`).",
				}
			}
			if priorBlocked(prior, "local.agent-service-detected") {
				return Result{
					Status: StatusSkip,
					Message: "skipped: no `azure.ai.agent` service in " +
						"azure.yaml (see check " +
						"`local.agent-service-detected`).",
				}
			}
			if priorBlocked(prior, "remote.auth") {
				return Result{
					Status: StatusSkip,
					Message: "skipped: auth probe did not succeed " +
						"(see check `remote.auth`).",
				}
			}
			if priorBlocked(prior, "remote.foundry-endpoint") {
				return Result{
					Status: StatusSkip,
					Message: "skipped: Foundry endpoint did not respond " +
						"(see check `remote.foundry-endpoint`).",
				}
			}
			if !priorPassed(prior, "remote.agent-status") {
				return Result{
					Status: StatusSkip,
					Message: "skipped: agent status check did not pass " +
						"(see check `remote.agent-status`).",
				}
			}
			endpoint := readProjectEndpoint(prior)
			if endpoint == "" {
				return Result{
					Status: StatusSkip,
					Message: "skipped: upstream check did not surface " +
						"AZURE_AI_PROJECT_ENDPOINT in its Details.",
				}
			}
			if apiVersion == "" {
				return Result{
					Status: StatusSkip,
					Message: "skipped: doctor wiring did not provide an " +
						"agent API version for the probe.",
				}
			}

			actives := readActiveAgents(prior)
			if len(actives) == 0 {
				return Result{
					Status: StatusSkip,
					Message: "skipped: no active agents reported by " +
						"`remote.agent-status`.",
				}
			}

			projectResourceID := ""
			var prErr error
			if deps.readProjectResourceIDFn != nil {
				projectResourceID, prErr = deps.readProjectResourceIDFn(ctx, deps.AzdClient)
			} else {
				projectResourceID, prErr = readProjectResourceID(ctx, deps.AzdClient)
			}
			if prErr != nil || projectResourceID == "" {
				return Result{
					Status: StatusSkip,
					Message: "skipped: AZURE_AI_PROJECT_ID is unset " +
						"(needed to scope role-assignment listing).",
				}
			}

			principalProbe := deps.probeAgentPrincipal
			if principalProbe == nil {
				principalProbe = makeRealProbeAgentPrincipal(apiVersion)
			}

			principals := fetchAllAgentPrincipals(
				ctx, actives, endpoint, principalProbe)

			query := deps.queryAgentIdentityRoles
			if query == nil {
				query = project.QueryAgentIdentityRoles
			}

			result, err := query(ctx, deps.AzdClient, projectResourceID, principals)
			if err != nil {
				if errors.Is(err, project.ErrInvalidProjectResourceID) {
					return Result{
						Status: StatusSkip,
						Message: "skipped: AZURE_AI_PROJECT_ID is " +
							"malformed (cannot derive role-assignment scopes).",
					}
				}
				return Result{
					Status: StatusWarn,
					Message: "could not list agent identity roles: " +
						firstLine(sanitizeScopeARNs(err.Error())),
					Suggestion: "Re-run `azd ai agent doctor` once the " +
						"transient failure clears.",
				}
			}

			entries := buildAgentIdentityRoleEntries(actives, result, opts.Unredacted)
			return classifyAgentIdentityRolesAggregate(entries, result.Scopes, opts.Unredacted)
		},
	}
}

// activeAgentMeta is the per-agent triple the check fans out across.
// AgentVersion is preserved verbatim from the prior
// `remote.agent-status` entry so the Detail rendering shows the
// same version string the user already saw on the previous check.
type activeAgentMeta struct {
	Service      string
	AgentName    string
	AgentVersion string
}

// readActiveAgents pulls the agent name/version triples for active
// agents out of the upstream `remote.agent-status` Details. Returns
// nil if the Details are missing or the wrong shape; the caller
// folds that into a Skip rather than guessing.
//
// Only entries with Classification `active` are returned — the
// design explicitly scopes this check to active agents, and feeding
// a Creating/Failed agent into a role-listing probe would fail with
// confusing "agent has no identity yet" errors.
func readActiveAgents(prior []Result) []activeAgentMeta {
	for _, p := range prior {
		if p.ID != "remote.agent-status" {
			continue
		}
		raw, ok := p.Details["services"]
		if !ok {
			return nil
		}
		entries, ok := raw.([]agentStatusEntry)
		if !ok {
			return nil
		}
		out := make([]activeAgentMeta, 0, len(entries))
		for _, e := range entries {
			if e.Classification != agentClassActive {
				continue
			}
			if e.AgentName == "" {
				continue
			}
			out = append(out, activeAgentMeta{
				Service:      e.Service,
				AgentName:    e.AgentName,
				AgentVersion: e.AgentVersion,
			})
		}
		return out
	}
	return nil
}

// fetchAllAgentPrincipals fans out principal-ID fetches with bounded
// concurrency. Order in the returned slice mirrors `actives` so the
// downstream Details rendering is deterministic.
func fetchAllAgentPrincipals(
	ctx context.Context,
	actives []activeAgentMeta,
	endpoint string,
	probe func(context.Context, string, string, string) agentIdentityProbeResult,
) []project.AgentPrincipal {
	out := make([]project.AgentPrincipal, len(actives))
	sem := make(chan struct{}, agentIdentityConcurrency)
	var wg sync.WaitGroup
	for i, a := range actives {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			probeCtx, cancel := context.WithTimeout(ctx, agentIdentityProbeTimeout)
			defer cancel()
			res := probe(probeCtx, endpoint, a.AgentName, a.AgentVersion)
			out[i] = project.AgentPrincipal{
				AgentName:    a.AgentName,
				AgentVersion: a.AgentVersion,
				PrincipalID:  res.PrincipalID,
			}
		})
	}
	wg.Wait()
	return out
}

// buildAgentIdentityRoleEntries folds the project.QueryAgentIdentityRoles
// output into the per-agent classification structs the aggregate
// classifier consumes. The function is total — every input agent
// produces an output entry — so the aggregate can rely on
// `len(entries) == len(actives)`.
func buildAgentIdentityRoleEntries(
	actives []activeAgentMeta,
	res *project.AgentIdentityRolesResult,
	unredacted bool,
) []agentIdentityRoleEntry {
	byName := make(map[string]project.AgentIdentityRolesEntry, len(res.Entries))
	for _, e := range res.Entries {
		byName[e.AgentName] = e
	}

	out := make([]agentIdentityRoleEntry, 0, len(actives))
	for _, a := range actives {
		entry := agentIdentityRoleEntry{
			AgentName:    a.AgentName,
			AgentVersion: a.AgentVersion,
		}
		qe, ok := byName[a.AgentName]
		if !ok {
			entry.Class = agentIdentityClassUnknown
			entry.Detail = fmt.Sprintf(
				"agent %q: role-assignment listing did not return.",
				a.AgentName)
			out = append(out, entry)
			continue
		}
		entry.PrincipalID = redactID(qe.PrincipalID, unredacted)
		if qe.PrincipalID == "" {
			entry.Class = agentIdentityClassUnknown
			entry.Detail = fmt.Sprintf(
				"agent %q: could not resolve managed-identity "+
					"principal ID from Foundry.",
				a.AgentName)
			out = append(out, entry)
			continue
		}

		entry.ProjectRoles = qe.ProjectScope.Roles
		entry.AccountRoles = qe.AccountScope.Roles
		entry.RGRoles = qe.RGScope.Roles
		if qe.ProjectScope.Err != nil {
			entry.ProjectErr = redactErrorText(qe.ProjectScope.Err.Error(), unredacted)
		}
		if qe.AccountScope.Err != nil {
			entry.AccountErr = redactErrorText(qe.AccountScope.Err.Error(), unredacted)
		}
		if qe.RGScope.Err != nil {
			entry.RGErr = redactErrorText(qe.RGScope.Err.Error(), unredacted)
		}

		entry.Class = classifyOneAgent(qe)
		entry.Detail = describeOneAgent(qe)
		out = append(out, entry)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].AgentName < out[j].AgentName
	})
	return out
}

// redactErrorText scrubs ARM scope ARNs and bare GUIDs out of an
// error string and returns its first line. When unredacted is true,
// the error's first line is returned verbatim so operators running
// `--unredacted` see the raw backend response. Centralized here so
// every per-scope error path applies the same masking sequence.
func redactErrorText(s string, unredacted bool) string {
	if unredacted {
		return firstLine(s)
	}
	return firstLine(sanitizeScopeARNs(s))
}

// classifyOneAgent buckets a single agent's per-scope listing into
// one of fine / underscoped / empty / unknown. A scope counts as
// "covered" when its Err is nil and Roles is non-empty. The
// pass-condition (per design): project covered AND (account
// covered OR RG covered).
func classifyOneAgent(qe project.AgentIdentityRolesEntry) string {
	projectCovered := qe.ProjectScope.Err == nil && len(qe.ProjectScope.Roles) > 0
	accountCovered := qe.AccountScope.Err == nil && len(qe.AccountScope.Roles) > 0
	rgCovered := qe.RGScope.Err == nil && len(qe.RGScope.Roles) > 0

	// All three probes errored — we can't classify.
	if qe.ProjectScope.Err != nil && qe.AccountScope.Err != nil && qe.RGScope.Err != nil {
		return agentIdentityClassUnknown
	}

	anyCovered := projectCovered || accountCovered || rgCovered
	if !anyCovered {
		return agentIdentityClassEmpty
	}
	if projectCovered && (accountCovered || rgCovered) {
		return agentIdentityClassFine
	}
	return agentIdentityClassUnderscoped
}

// describeOneAgent renders the one-line per-agent Detail. Format:
//
//	<agent>: project=N, account=M, resource-group=K
//
// with `?` for probe-error scopes. Designed to fit on one line of an
// 80-col terminal even with 4-char wide role names plus the lead
// `<agent>: ` prefix.
func describeOneAgent(qe project.AgentIdentityRolesEntry) string {
	return fmt.Sprintf(
		"%s: project=%s, account=%s, resource-group=%s",
		qe.AgentName,
		formatScopeCount(qe.ProjectScope),
		formatScopeCount(qe.AccountScope),
		formatScopeCount(qe.RGScope),
	)
}

// formatScopeCount renders one scope's count for describeOneAgent.
// Returns the literal "?" when the per-scope probe errored — the
// row tells the user the listing is incomplete without needing to
// open Details.
func formatScopeCount(sr project.AgentScopeRoles) string {
	if sr.Err != nil {
		return "?"
	}
	return fmt.Sprintf("%d", len(sr.Roles))
}

// classifyAgentIdentityRolesAggregate folds the per-agent entries
// into a single doctor Result. The aggregate Status is the worst
// per-agent Class's bucket; the Message picks the worst entry for
// the headline and the per-agent breakdown lands in Details. A
// remediation suggestion is attached only to FAIL and WARN
// classes — the INFO state has nothing actionable to offer.
//
// Empty entries (no active agents to enumerate) are folded into
// Skip upstream; if the function is somehow reached with an empty
// slice it produces a Skip rather than a Pass to avoid emitting an
// empty INFO line.
func classifyAgentIdentityRolesAggregate(
	entries []agentIdentityRoleEntry,
	scopes project.AgentIdentityScopes,
	unredacted bool,
) Result {
	if len(entries) == 0 {
		return Result{
			Status:  StatusSkip,
			Message: "no active agents to enumerate.",
		}
	}

	worst := agentIdentityClassFine
	for _, e := range entries {
		if rankAgentIdentity(e.Class) > rankAgentIdentity(worst) {
			worst = e.Class
		}
	}

	byClass := map[string]int{}
	for _, e := range entries {
		byClass[e.Class]++
	}

	details := map[string]any{
		"agents":           entries,
		"byClassification": byClass,
		"scopes": map[string]string{
			"project":        redactScope(scopes.Project, unredacted),
			"account":        redactScope(scopes.Account, unredacted),
			"resource-group": redactScope(scopes.ResourceGroup, unredacted),
		},
	}

	detailLines := func() []string {
		out := make([]string, 0, len(entries))
		for _, e := range entries {
			out = append(out, e.Detail)
		}
		return out
	}

	switch worst {
	case agentIdentityClassFine:
		return Result{
			Status: StatusInfo,
			Message: fmt.Sprintf(
				"%d of %d agents have role assignments at the "+
					"project scope plus at least one of "+
					"account/resource-group scope.",
				byClass[agentIdentityClassFine], len(entries)),
			Details: details,
			Suggestion: "Role assignments listed; no action needed. " +
				"Use `azd ai agent doctor --output json` for the " +
				"machine-readable per-agent breakdown.\n  " +
				strings.Join(detailLines(), "\n  "),
		}
	case agentIdentityClassUnknown:
		// Pure-unknown aggregate: every agent had a probe failure.
		// Surface as WARN with a re-run hint.
		return Result{
			Status: StatusWarn,
			Message: fmt.Sprintf(
				"%d of %d agents: could not list role assignments.",
				byClass[agentIdentityClassUnknown], len(entries)),
			Details: details,
			Suggestion: "Re-run `azd ai agent doctor` once the " +
				"transient failure clears. Per-agent detail:\n  " +
				strings.Join(detailLines(), "\n  "),
		}
	case agentIdentityClassUnderscoped:
		// At least one agent is underscoped (assignments exist
		// somewhere but pass condition unmet).
		return Result{
			Status: StatusWarn,
			Message: fmt.Sprintf(
				"%d of %d agents have under-privileged role assignments.",
				byClass[agentIdentityClassUnderscoped], len(entries)),
			Details: details,
			Suggestion: "Agents may not have permission to access " +
				"project / account resources at runtime. Grant " +
				"a role on the missing scope:\n  " +
				"az role assignment create --assignee <mi-principal-id> " +
				"--role <role> --scope <project-or-account-scope>\n" +
				"Per-agent detail:\n  " +
				strings.Join(detailLines(), "\n  "),
		}
	case agentIdentityClassEmpty:
		return Result{
			Status: StatusFail,
			Message: fmt.Sprintf(
				"%d of %d agents have zero role assignments at any "+
					"reachable scope.",
				byClass[agentIdentityClassEmpty], len(entries)),
			Details: details,
			Suggestion: "Agents will likely 403 on every tool call. " +
				"Grant Cognitive Services User (or stronger) on " +
				"the project scope:\n  " +
				"az role assignment create --assignee <mi-principal-id> " +
				"--role \"Cognitive Services User\" --scope " +
				"<project-scope>\nPer-agent detail:\n  " +
				strings.Join(detailLines(), "\n  "),
		}
	default:
		// Defensive: an unrecognized class collapses to WARN with
		// the raw class string surfaced for diagnostic purposes.
		return Result{
			Status: StatusWarn,
			Message: fmt.Sprintf(
				"unrecognized aggregate class %q.", worst),
			Details: details,
		}
	}
}

func rankAgentIdentity(class string) int {
	if r, ok := agentIdentityClassRank[class]; ok {
		return r
	}
	return -1
}

// makeRealProbeAgentPrincipal returns the production closure used
// to fetch one agent's `instance_identity.principal_id`. Mirrors
// makeRealProbeAgentStatus from checks_agent_status.go (same
// credential and SDK client; different response field consumed)
// so a Pass here matches what the runtime invoke flow would see.
func makeRealProbeAgentPrincipal(
	apiVersion string,
) func(context.Context, string, string, string) agentIdentityProbeResult {
	return func(
		ctx context.Context,
		endpoint, agentName, agentVersion string,
	) agentIdentityProbeResult {
		cred, err := azidentity.NewAzureDeveloperCLICredential(
			&azidentity.AzureDeveloperCLICredentialOptions{},
		)
		if err != nil {
			return agentIdentityProbeResult{
				Err: fmt.Errorf("create credential: %w", err),
			}
		}
		client := agent_api.NewAgentClient(endpoint, cred)
		v, err := client.GetAgentVersion(
			ctx, agentName, agentVersion, apiVersion)
		if err != nil {
			if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
				return agentIdentityProbeResult{
					StatusCode: respErr.StatusCode,
					Err:        err,
				}
			}
			return agentIdentityProbeResult{Err: err}
		}
		if v == nil {
			return agentIdentityProbeResult{
				Err: errors.New("GetAgentVersion returned nil"),
			}
		}
		if v.InstanceIdentity == nil {
			return agentIdentityProbeResult{
				StatusCode: 200,
				Err: fmt.Errorf("agent %q has no instance_identity",
					agentName),
			}
		}
		return agentIdentityProbeResult{
			StatusCode:  200,
			PrincipalID: v.InstanceIdentity.PrincipalID,
		}
	}
}

// priorPassed reports whether a prior check with the given ID
// produced StatusPass. False both for "check not in slice" and
// "check present but didn't pass" — callers handle the two
// outcomes the same way (Skip).
func priorPassed(prior []Result, id string) bool {
	for _, p := range prior {
		if p.ID == id {
			return p.Status == StatusPass
		}
	}
	return false
}
