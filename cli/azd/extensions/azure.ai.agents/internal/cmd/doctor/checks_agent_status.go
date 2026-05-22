// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// agentStatusProbeTimeout caps the per-service Foundry round trip.
// The check fans out one GET per hosted-agent service in azure.yaml
// concurrently with bounded worker pool (probeConcurrency); the
// per-probe ceiling is held shorter than the foundry-endpoint
// reachability probe (which only ever runs once). 6 s gives a
// stalled DNS / VPN time to surface as a clean timeout without
// making a 5-service project drag the whole doctor run past half a
// minute.
const agentStatusProbeTimeout = 6 * time.Second

// probeConcurrency bounds the agent-status probe fan-out. The
// design spec calls for parallel probes ("fan out one GET per
// hosted-agent service") so wall-clock cost stays bounded by the
// slowest probe, not the sum. 4 workers balances responsiveness
// against the risk of overwhelming Foundry's per-token rate limit
// when a project declares dozens of services.
const probeConcurrency = 4

// agentStatusKindActive / Creating / Failed / Deleting are the
// canonical lifecycle values emitted by the Foundry agents service
// (vienna:
// `Contracts/V2/Generated/Agents/AgentVersionStatus.cs`). Matched
// case-insensitively because Foundry has historically been
// inconsistent about casing on similar fields (e.g., the run.go
// invocation flow normalizes status with `strings.EqualFold`).
const (
	agentStatusActive   = "Active"
	agentStatusCreating = "Creating"
	agentStatusFailed   = "Failed"
	agentStatusDeleting = "Deleting"
	agentStatusDeleted  = "Deleted"
)

// agentStatusProbeResult is the structured outcome of one
// `GetAgentVersion` call. statusCode is the HTTP code (0 when the
// request never reached Foundry — DNS, TLS, context timeout, etc.).
// status is the lifecycle string from the response body when the
// call returned a 200; empty in any other case. err is the raw
// transport / SDK error.
//
// We intentionally don't surface the full AgentVersionObject —
// the doctor only needs the lifecycle + HTTP status to classify.
// Keeping the surface narrow protects callers from coupling to the
// AgentVersionObject shape (which evolves with the Foundry API).
type agentStatusProbeResult struct {
	statusCode int
	status     string
	err        error
}

// agentStatusEntry captures the per-service outcome of the probe.
// It is the unit the aggregate-classifier consumes; we keep this
// internal so future evolution (e.g., adding role information)
// does not break callers that only need the lifecycle. The struct
// is also surfaced verbatim under `Details["services"]` so JSON
// consumers can iterate without re-parsing the human-readable
// Message.
type agentStatusEntry struct {
	Service      string `json:"service"`
	AgentName    string `json:"agentName,omitempty"`
	AgentVersion string `json:"agentVersion,omitempty"`
	Status       string `json:"status,omitempty"`
	HTTPStatus   int    `json:"httpStatus,omitempty"`
	// Classification is the doctor-level bucket. Distinct from
	// Status because the aggregate logic branches on this, not on
	// the Foundry lifecycle name (which can grow without notice).
	Classification string `json:"classification"`
	// Detail is the per-service human-readable explanation. The
	// aggregate Message picks the worst-classification's Detail
	// when emitting a one-line summary; the full per-service list
	// always lands in Details.
	Detail string `json:"detail,omitempty"`
}

// Per-service classifications. These map onto doctor.Status with
// the aggregate rules in `classifyAgentStatusAggregate`. The
// ordering used by `worseClassification` is encoded by the integer
// values: higher = worse.
const (
	agentClassActive       = "active"
	agentClassDeploying    = "deploying"
	agentClassFailed       = "failed"
	agentClassMissing      = "missing"      // 404 / agent not found
	agentClassNotDeployed  = "not-deployed" // AGENT_<KEY>_NAME absent
	agentClassUnknown      = "unknown"      // unrecognized status string
	agentClassTransientErr = "transient"    // probe error (skipped, not failed)
)

// agentClassRank gives a strict ordering for the aggregate
// classifier: the highest rank present drives the aggregate
// Status. Ties on `failed`/`missing` collapse to a single
// fail-class Suggestion ("see service-specific details below").
//
// `active` is the floor; `transient` sits just above active so a
// run that mixes a Pass with a Skip-class entry surfaces the
// Skip-class entry's Suggestion but doesn't turn the whole check
// red (a transient probe failure is genuinely a Skip for that
// service).
var agentClassRank = map[string]int{
	agentClassActive:       0,
	agentClassTransientErr: 1,
	agentClassDeploying:    2,
	agentClassNotDeployed:  3,
	agentClassUnknown:      4,
	agentClassMissing:      5,
	agentClassFailed:       6,
}

// newCheckAgentStatus produces Check `remote.agent-status`. For
// each hosted-agent service in azure.yaml it resolves the
// deployed agent name / version from the active azd environment
// (the values written by `service_target_agent.go` after a
// successful `azd deploy`) and probes Foundry's
// `GET /agents/{name}/versions/{version}` endpoint.
//
// Per-service classification is exhaustive:
//
//   - Active → Pass for that service.
//   - Creating → Warn (`monitor --follow` for live progress).
//   - Failed → Fail (`monitor --follow` to read logs, NOT
//     `azd deploy` — redeploying without understanding the failure
//     just re-creates the same broken version).
//   - 404 / Deleted → Fail (`azd deploy`).
//   - AGENT_<KEY>_NAME absent, or NAME set but AGENT_<KEY>_VERSION
//     absent → Fail (`azd deploy`) — the service is declared but
//     has never been (fully) deployed; the user knows what to do.
//   - Unrecognized status → Fail with the raw status string so
//     the user can search for it; suggests inspecting the agent in
//     the Foundry portal.
//   - Probe error (network, 401, 403, 5xx, …) → service-scoped
//     skip; aggregate classifier keeps it from failing the run if
//     other services are healthy.
//
// Aggregate rule (see `classifyAgentStatusAggregate`):
//
//   - All Active → Pass.
//   - Worst class is `failed` / `missing` / `unknown` / `not-deployed`
//     → Fail.
//   - Worst class is `deploying` → Warn.
//   - Worst class is `transient` and at least one Active → Pass
//     (with a Message note that some services were skipped).
//   - All `transient` → Skip.
//
// When multiple failing classes coexist (e.g., `failed` and
// `missing` together), the dominant class drives the headline and
// Suggestion. A short "other agents have additional issues" hint is
// appended to the Suggestion so the user knows to read Details for
// the second-priority fix path.
//
// Skip-cascade (per design dependency matrix lines 110-117 of
// azd-ai-agent-doctor-remote-checks.md):
//
//  1. `local.environment-selected` — env reads would Fail.
//  2. `local.agent-service-detected` — without a service list
//     there's nothing to probe; cascade rather than emit a Pass
//     ("0 of 0 agents active") which reads as a bug.
//  3. `remote.auth` — without a valid token every probe would 401.
//  4. `remote.foundry-endpoint` — if the endpoint isn't reachable
//     none of the probes will land; surface a single Skip rather
//     than `N×` identical transport errors.
//
// We deliberately do NOT cascade from `remote.rbac`. Agent-list /
// agent-get is a Reader-level operation; a developer with read
// access but no deploy role can still see whether their agents
// are healthy, and surfacing that information is the whole point
// of the check.
func newCheckAgentStatus(deps Dependencies) Check {
	apiVersion := deps.AgentAPIVersion
	return Check{
		ID:     "remote.agent-status",
		Name:   "Hosted agents are active",
		Remote: true,
		Fn: func(ctx context.Context, _ Options, prior []Result) Result {
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
			endpoint := readProjectEndpoint(prior)
			if endpoint == "" {
				return Result{
					Status: StatusSkip,
					Message: "skipped: upstream check passed but did not " +
						"surface AZURE_AI_PROJECT_ENDPOINT in its Details.",
				}
			}
			if apiVersion == "" {
				return Result{
					Status: StatusSkip,
					Message: "skipped: doctor wiring did not provide an " +
						"agent API version for the probe.",
				}
			}
			services := readAgentServices(prior)
			if len(services) == 0 {
				return Result{
					Status: StatusSkip,
					Message: "skipped: upstream check passed but did not " +
						"surface agent service names in its Details.",
				}
			}

			nameVersionReader := deps.readAgentNameVersionFn
			if nameVersionReader == nil {
				nameVersionReader = readAgentNameVersion
			}
			probe := deps.probeAgentStatus
			if probe == nil {
				probe = makeRealProbeAgentStatus(apiVersion)
			}

			entries := probeAllServices(
				ctx, deps.AzdClient, services, endpoint,
				nameVersionReader, probe)

			return classifyAgentStatusAggregate(entries)
		},
	}
}

// probeOneService is the per-service body of the check loop.
// Factored out so unit tests can drive a single service without
// reconstructing the surrounding closure; production callers should
// use the parent `newCheckAgentStatus` rather than calling this
// directly.
func probeOneService(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	serviceName, endpoint string,
	readNameVersion func(context.Context, *azdext.AzdClient, string) (string, string, error),
	probe func(context.Context, string, string, string) agentStatusProbeResult,
) agentStatusEntry {
	entry := agentStatusEntry{Service: serviceName}

	name, ver, err := readNameVersion(ctx, azdClient, serviceName)
	if err != nil {
		entry.Classification = agentClassTransientErr
		entry.Detail = fmt.Sprintf(
			"could not read deployed agent name/version: %s",
			firstLine(err.Error()))
		return entry
	}
	if name == "" {
		entry.Classification = agentClassNotDeployed
		entry.Detail = fmt.Sprintf(
			"service %q has not been deployed yet "+
				"(AGENT_%s_NAME is unset).",
			serviceName, doctorServiceKey(serviceName))
		return entry
	}
	entry.AgentName = name
	if ver == "" {
		// We have a deployed name but no version. This is an
		// inconsistent state — the post-deploy hook writes both
		// vars atomically, so a present-NAME / absent-VERSION
		// means deployment never completed (or env was edited by
		// hand). Treat as not-deployed so the user is directed to
		// re-run `azd deploy` rather than told to retry doctor.
		entry.Classification = agentClassNotDeployed
		entry.Detail = fmt.Sprintf(
			"service %q has AGENT_%s_NAME set but "+
				"AGENT_%s_VERSION is missing; the previous "+
				"deployment did not complete cleanly.",
			serviceName,
			doctorServiceKey(serviceName), doctorServiceKey(serviceName))
		return entry
	}
	entry.AgentVersion = ver

	probeCtx, cancel := context.WithTimeout(ctx, agentStatusProbeTimeout)
	defer cancel()
	res := probe(probeCtx, endpoint, name, ver)
	entry.HTTPStatus = res.statusCode
	entry.Status = res.status

	switch {
	case errors.Is(res.err, context.Canceled):
		entry.Classification = agentClassTransientErr
		entry.Detail = "probe was cancelled."
		return entry
	case errors.Is(res.err, context.DeadlineExceeded):
		entry.Classification = agentClassTransientErr
		entry.Detail = fmt.Sprintf(
			"probe did not respond within %s.",
			agentStatusProbeTimeout)
		return entry
	case res.statusCode == http.StatusNotFound:
		entry.Classification = agentClassMissing
		entry.Detail = fmt.Sprintf(
			"agent %q (version %s) was not found on the Foundry project.",
			name, ver)
		return entry
	case res.err != nil:
		entry.Classification = agentClassTransientErr
		entry.Detail = fmt.Sprintf(
			"probe failed: %s",
			firstLine(res.err.Error()))
		return entry
	}

	// Status branch. Match case-insensitively because Foundry has
	// shipped both Pascal-cased and lower-cased lifecycle values
	// historically; the production invoke flow normalizes with
	// EqualFold.
	switch {
	case strings.EqualFold(res.status, agentStatusActive):
		entry.Classification = agentClassActive
		entry.Detail = fmt.Sprintf("agent active (v%s).", ver)
	case strings.EqualFold(res.status, agentStatusCreating):
		entry.Classification = agentClassDeploying
		entry.Detail = fmt.Sprintf(
			"agent is still deploying (v%s).", ver)
	case strings.EqualFold(res.status, agentStatusFailed):
		entry.Classification = agentClassFailed
		entry.Detail = fmt.Sprintf(
			"agent deployment failed (v%s).", ver)
	case strings.EqualFold(res.status, agentStatusDeleting),
		strings.EqualFold(res.status, agentStatusDeleted):
		entry.Classification = agentClassMissing
		entry.Detail = fmt.Sprintf(
			"agent has been deleted or is being deleted (v%s).", ver)
	default:
		entry.Classification = agentClassUnknown
		entry.Detail = fmt.Sprintf(
			"agent in unrecognized status %q (v%s).", res.status, ver)
	}
	return entry
}

// probeAllServices runs probeOneService across `services` with
// bounded concurrency (probeConcurrency workers). Order in the
// returned slice mirrors the input `services` order so that the
// aggregate's downstream sort + Details rendering remain
// deterministic regardless of which worker finishes first.
//
// The function does not introduce a separate timeout — each
// probeOneService call enforces its own per-probe timeout via
// agentStatusProbeTimeout, and the parent ctx propagates
// cancellation to all in-flight workers.
func probeAllServices(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	services []string,
	endpoint string,
	readNameVersion func(context.Context, *azdext.AzdClient, string) (string, string, error),
	probe func(context.Context, string, string, string) agentStatusProbeResult,
) []agentStatusEntry {
	entries := make([]agentStatusEntry, len(services))
	sem := make(chan struct{}, probeConcurrency)
	var wg sync.WaitGroup
	for i, svc := range services {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			entries[i] = probeOneService(
				ctx, azdClient, svc, endpoint,
				readNameVersion, probe)
		})
	}
	wg.Wait()
	return entries
}

// classifyAgentStatusAggregate folds the per-service entries into a
// single doctor Result. The aggregate Status is the worst
// per-service Classification's bucket; the Message lists each
// failing service one line at a time (truncated to 3 with a
// trailing "(N more)" if needed); the Suggestion targets the
// dominant fix path.
//
// We list services even when they're healthy in the Pass message,
// so a user running the check sees what was probed.
//
// Heterogeneous failing classes (e.g., one Failed agent and one
// Missing agent in the same project) collapse to the
// highest-ranked class for headline/count purposes, but the
// detail lines are filtered to that dominant class only so the
// headline count and the rendered body always agree. A short hint
// is appended to the Suggestion telling the user to read Details
// for the secondary class.
func classifyAgentStatusAggregate(entries []agentStatusEntry) Result {
	// Sort for deterministic Message / Details rendering.
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Service < entries[j].Service
	})

	// Find the worst classification present.
	worst := agentClassActive
	for _, e := range entries {
		if rank(e.Classification) > rank(worst) {
			worst = e.Classification
		}
	}

	// Tally per-class for the summary and for the "all Active /
	// some skipped" mix-case branch.
	byClass := map[string]int{}
	for _, e := range entries {
		byClass[e.Classification]++
	}

	details := map[string]any{
		"services":         entries,
		"byClassification": byClass,
	}

	total := len(entries)

	// detailsForClass returns "{service}: {detail}" lines for
	// exactly the given class. The headline count uses
	// byClass[worst]; using class-filtered detail lines here keeps
	// the rendered body in sync with that count even when entries
	// from multiple failing classes are present.
	detailsForClass := func(class string) []string {
		out := make([]string, 0, byClass[class])
		for _, e := range entries {
			if e.Classification == class {
				out = append(out, fmt.Sprintf("%s: %s", e.Service, e.Detail))
			}
		}
		return out
	}

	// otherFailingClasses returns the non-{worst, active, transient}
	// classes that are also present, sorted for determinism. Used
	// to enrich the dominant Suggestion when the run contains a
	// mix of failing classes.
	otherFailingClasses := func() []string {
		out := []string{}
		for class, n := range byClass {
			if n == 0 ||
				class == worst ||
				class == agentClassActive ||
				class == agentClassTransientErr {
				continue
			}
			out = append(out, class)
		}
		sort.Strings(out)
		return out
	}

	// appendOthersHint enriches a Suggestion when entries from
	// other failing classes coexist with the dominant class.
	appendOthersHint := func(s string) string {
		others := otherFailingClasses()
		if len(others) == 0 {
			return s
		}
		return s + " Other agents have additional issues " +
			"(" + strings.Join(others, ", ") + "); " +
			"see the per-service Details for the full list."
	}

	switch worst {
	case agentClassActive:
		// All healthy.
		names := serviceNamesByClass(entries, agentClassActive)
		return Result{
			Status: StatusPass,
			Message: fmt.Sprintf(
				"%d of %d agents active: %s.",
				byClass[agentClassActive], total,
				strings.Join(names, ", ")),
			Details: details,
		}
	case agentClassTransientErr:
		// Per the documented aggregate rule, a transient probe
		// failure for one service should not mask the healthy
		// status of the rest — when at least one Active is
		// present we surface a Pass with a Message note that
		// some probes were skipped.
		if byClass[agentClassActive] > 0 {
			return Result{
				Status: StatusPass,
				Message: fmt.Sprintf(
					"%d of %d agents active; %d probe(s) skipped: %s",
					byClass[agentClassActive], total,
					byClass[agentClassTransientErr],
					firstTransient(entries)),
				Details: details,
			}
		}
		// All probes skipped — surface as Skip with the highest-
		// signal detail.
		return Result{
			Status: StatusSkip,
			Message: fmt.Sprintf(
				"skipped: %d agent probe(s) did not complete: %s",
				byClass[agentClassTransientErr], firstTransient(entries)),
			Suggestion: "Retry `azd ai agent doctor` after a moment; if " +
				"the failure persists, verify Foundry reachability and " +
				"that the agents have been deployed.",
			Details: details,
		}
	case agentClassDeploying:
		return Result{
			Status: StatusWarn,
			Message: fmt.Sprintf(
				"%d of %d agents are still deploying: %s.",
				byClass[agentClassDeploying], total,
				strings.Join(serviceNamesByClass(entries, agentClassDeploying), ", ")),
			Suggestion: appendOthersHint("Watch progress with " +
				"`azd ai agent monitor --follow`; the agent is not yet " +
				"available for invocation."),
			Details: details,
		}
	case agentClassNotDeployed:
		return Result{
			Status: StatusFail,
			Message: fmt.Sprintf(
				"%d of %d agents have not been deployed:\n  %s",
				byClass[agentClassNotDeployed], total,
				strings.Join(truncateLines(detailsForClass(agentClassNotDeployed), 3), "\n  ")),
			Suggestion: appendOthersHint("Run `azd deploy` to deploy the missing agents."),
			Details:    details,
		}
	case agentClassMissing:
		return Result{
			Status: StatusFail,
			Message: fmt.Sprintf(
				"%d of %d agents are missing on the Foundry project:\n  %s",
				byClass[agentClassMissing], total,
				strings.Join(truncateLines(detailsForClass(agentClassMissing), 3), "\n  ")),
			Suggestion: appendOthersHint("Run `azd deploy` to re-create the missing " +
				"agent(s) on the Foundry project."),
			Details: details,
		}
	case agentClassFailed:
		return Result{
			Status: StatusFail,
			Message: fmt.Sprintf(
				"%d of %d agents are in a failed state:\n  %s",
				byClass[agentClassFailed], total,
				strings.Join(truncateLines(detailsForClass(agentClassFailed), 3), "\n  ")),
			Suggestion: appendOthersHint("Inspect the failure with " +
				"`azd ai agent monitor --follow` to read the deploy " +
				"logs; redeploy only after addressing the root cause."),
			Details: details,
		}
	case agentClassUnknown:
		return Result{
			Status: StatusFail,
			Message: fmt.Sprintf(
				"%d of %d agents reported an unrecognized status:\n  %s",
				byClass[agentClassUnknown], total,
				strings.Join(truncateLines(detailsForClass(agentClassUnknown), 3), "\n  ")),
			Suggestion: appendOthersHint("Inspect the agent in the Foundry portal; if " +
				"the status looks healthy there, this is likely a " +
				"transient Foundry / extension mismatch — retry " +
				"`azd ai agent doctor` after a moment."),
			Details: details,
		}
	default:
		// Defensive: agentClassRank covers every constant in this
		// file; reaching this branch would mean a constant was
		// added without updating the rank map. Surface as a
		// transient Skip so the user gets a sane next step.
		return Result{
			Status: StatusSkip,
			Message: fmt.Sprintf(
				"skipped: doctor encountered an unhandled "+
					"classification %q.", worst),
		}
	}
}

// rank returns the configured rank of a per-service classification,
// or 0 (active) when unknown. We never want a missing-from-map
// classification to drive the aggregate Status, so the safe default
// is the floor.
func rank(class string) int {
	if r, ok := agentClassRank[class]; ok {
		return r
	}
	return 0
}

// serviceNamesByClass returns the service names whose
// Classification matches the given class, preserving the input
// order (which is already sorted by the aggregate caller).
func serviceNamesByClass(entries []agentStatusEntry, class string) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Classification == class {
			out = append(out, e.Service)
		}
	}
	return out
}

// firstTransient returns the first transient-class entry's Detail
// to use as the headline message when every service skipped.
// Returns "no diagnostic detail" if no transient entry has a non-
// empty Detail (defensive — production code always populates Detail).
func firstTransient(entries []agentStatusEntry) string {
	for _, e := range entries {
		if e.Classification == agentClassTransientErr && e.Detail != "" {
			return e.Detail
		}
	}
	return "no diagnostic detail"
}

// truncateLines collapses a long slice into at most max entries,
// appending an "(N more)" sentinel if the input was longer. Used
// to keep the aggregate Message bounded when a project has many
// failing agents.
func truncateLines(lines []string, max int) []string {
	if len(lines) <= max {
		return lines
	}
	out := make([]string, 0, max+1)
	out = append(out, lines[:max]...)
	out = append(out, fmt.Sprintf("(%d more)", len(lines)-max))
	return out
}

// readAgentServices pulls the agent service name list out of the
// upstream `local.agent-service-detected` check's Details. Returns
// nil when the upstream check did not surface the field (e.g.,
// because it Skipped or because its shape was refactored). The
// caller is responsible for deciding whether nil = Skip; we don't
// guess here because there's no safe default.
func readAgentServices(prior []Result) []string {
	for _, p := range prior {
		if p.ID != "local.agent-service-detected" {
			continue
		}
		v, ok := p.Details["agentServices"].([]string)
		if !ok {
			return nil
		}
		return v
	}
	return nil
}

// readAgentNameVersion pulls AGENT_<KEY>_NAME and AGENT_<KEY>_VERSION
// out of the active azd environment for the given service. Returns
// the trimmed values verbatim — an empty string from either field
// means the variable was unset, which the caller distinguishes from
// a transport error.
//
// EnvName is intentionally left empty: the gRPC service resolves
// "" to the currently-active env (see
// `internal/grpcserver/environment_service.go:GetValue`), which is
// the same env any subsequent `azd deploy` would write to.
func readAgentNameVersion(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	serviceName string,
) (string, string, error) {
	key := doctorServiceKey(serviceName)
	nameKey := fmt.Sprintf("AGENT_%s_NAME", key)
	verKey := fmt.Sprintf("AGENT_%s_VERSION", key)

	nameResp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		Key: nameKey,
	})
	if err != nil {
		return "", "", fmt.Errorf("read %s: %w", nameKey, err)
	}
	verResp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		Key: verKey,
	})
	if err != nil {
		return "", "", fmt.Errorf("read %s: %w", verKey, err)
	}
	name := ""
	if nameResp != nil {
		name = strings.TrimSpace(nameResp.Value)
	}
	ver := ""
	if verResp != nil {
		ver = strings.TrimSpace(verResp.Value)
	}
	return name, ver, nil
}

// doctorServiceKey converts a service name into the env var key
// format (uppercase, underscores). Mirrors `cmd.toServiceKey` —
// duplicated here because the doctor package cannot import the
// parent `cmd` package without forming an import cycle (the same
// rationale as for `agentHost` in checks_project.go). Must stay
// in sync with `cmd/helpers.go:680`.
func doctorServiceKey(serviceName string) string {
	key := strings.ReplaceAll(serviceName, " ", "_")
	key = strings.ReplaceAll(key, "-", "_")
	return strings.ToUpper(key)
}

// makeRealProbeAgentStatus returns the production probe closure for
// the given api-version. It builds a credential via the same
// `NewAzureDeveloperCLICredential` path used by `agent_context.go`
// (so a Pass here matches what the runtime invoke flow needs) and
// invokes `agent_api.GetAgentVersion`.
//
// The closure handles HTTP-status / transport / response-body
// classification by sniffing the SDK error: `azcore.ResponseError`
// exposes `StatusCode`, which we surface in `statusCode` so the
// caller can route 404s onto the missing-class branch without
// re-parsing error strings.
func makeRealProbeAgentStatus(
	apiVersion string,
) func(context.Context, string, string, string) agentStatusProbeResult {
	return func(
		ctx context.Context,
		endpoint, agentName, agentVersion string,
	) agentStatusProbeResult {
		cred, err := azidentity.NewAzureDeveloperCLICredential(
			&azidentity.AzureDeveloperCLICredentialOptions{},
		)
		if err != nil {
			return agentStatusProbeResult{
				err: fmt.Errorf("create credential: %w", err),
			}
		}

		client := agent_api.NewAgentClient(endpoint, cred)
		v, err := client.GetAgentVersion(
			ctx, agentName, agentVersion, apiVersion)
		if err != nil {
			if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
				return agentStatusProbeResult{
					statusCode: respErr.StatusCode,
					err:        err,
				}
			}
			return agentStatusProbeResult{err: err}
		}
		if v == nil {
			return agentStatusProbeResult{
				err: errors.New("GetAgentVersion returned nil"),
			}
		}
		return agentStatusProbeResult{
			statusCode: http.StatusOK,
			status:     v.Status,
		}
	}
}
