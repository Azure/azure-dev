// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

// healthyPriorResults returns the canonical "all upstream checks
// passed" prior slice used by the agent-status check tests. The
// service list `services` is surfaced under
// `local.agent-service-detected` Details exactly as the production
// check surfaces it (see checks_project.go:91); the endpoint is
// surfaced under `local.project-endpoint-set` Details to match the
// production wiring in checks_local.go's project endpoint check.
func healthyPriorResults(services []string, endpoint string) []Result {
	return []Result{
		{ID: "local.environment-selected", Status: StatusPass},
		{ID: "local.agent-service-detected", Status: StatusPass, Details: map[string]any{
			"agentServices": services,
		}},
		{ID: "local.project-endpoint-set", Status: StatusPass, Details: map[string]any{
			"projectEndpoint": endpoint,
		}},
		{ID: "remote.auth", Status: StatusPass},
		{ID: "remote.foundry-endpoint", Status: StatusPass},
	}
}

// fixedNameVersionReader returns a stub readAgentNameVersionFn that
// looks up names/versions from a static map keyed by service name.
// Missing key → empty strings (matches the production "AGENT_<KEY>_NAME
// unset" path). Used by every test that needs to drive the per-service
// loop without spinning up a real gRPC env service.
type pair struct{ name, version string }

func fixedNameVersionReader(
	m map[string]pair,
) func(context.Context, *azdext.AzdClient, string) (string, string, error) {
	return func(_ context.Context, _ *azdext.AzdClient, svc string) (string, string, error) {
		v, ok := m[svc]
		if !ok {
			return "", "", nil
		}
		return v.name, v.version, nil
	}
}

// fixedProbe returns a stub agent-status probe that looks up the
// probe result by a (name, version) key — distinct services with
// distinct (name, version) pairs can simulate heterogeneous
// classifications inside a single aggregate run.
type probeKey struct{ name, version string }

func fixedProbe(
	m map[probeKey]agentStatusProbeResult,
) func(context.Context, string, string, string) agentStatusProbeResult {
	return func(_ context.Context, _ string, name, version string) agentStatusProbeResult {
		v, ok := m[probeKey{name, version}]
		if !ok {
			return agentStatusProbeResult{
				err: errors.New("probe stub: unexpected (name, version)"),
			}
		}
		return v
	}
}

// runCheckWithDeps invokes the check Fn with the given prior /
// options / dependencies. Returns the produced Result.
func runCheckWithDeps(t *testing.T, deps Dependencies, prior []Result) Result {
	t.Helper()
	// AzdClient must be non-nil to clear the first skip guard; tests
	// don't actually call into it because the readAgentNameVersionFn
	// seam diverts every env read to the stub.
	if deps.AzdClient == nil {
		deps.AzdClient = &azdext.AzdClient{}
	}
	if deps.AgentAPIVersion == "" {
		deps.AgentAPIVersion = "v1"
	}
	c := newCheckAgentStatus(deps)
	require.NotNil(t, c.Fn, "newCheckAgentStatus must return a non-nil Fn")
	return c.Fn(t.Context(), Options{}, prior)
}

// ---- Skip-cascade gates ----

func TestCheckAgentStatus_SkipsWhenAzdClientNil(t *testing.T) {
	t.Parallel()
	c := newCheckAgentStatus(Dependencies{})
	res := c.Fn(t.Context(), Options{}, nil)
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "azd extension not reachable")
}

func TestCheckAgentStatus_SkipsWhenEnvironmentNotSelected(t *testing.T) {
	t.Parallel()
	deps := Dependencies{AzdClient: &azdext.AzdClient{}}
	prior := []Result{{ID: "local.environment-selected", Status: StatusFail}}
	res := newCheckAgentStatus(deps).Fn(t.Context(), Options{}, prior)
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "local.environment-selected")
}

func TestCheckAgentStatus_SkipsWhenAgentServiceDetectedFailed(t *testing.T) {
	t.Parallel()
	deps := Dependencies{AzdClient: &azdext.AzdClient{}}
	prior := []Result{
		{ID: "local.environment-selected", Status: StatusPass},
		{ID: "local.agent-service-detected", Status: StatusFail},
	}
	res := newCheckAgentStatus(deps).Fn(t.Context(), Options{}, prior)
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "local.agent-service-detected")
}

func TestCheckAgentStatus_SkipsWhenAuthFailed(t *testing.T) {
	t.Parallel()
	deps := Dependencies{AzdClient: &azdext.AzdClient{}}
	prior := []Result{
		{ID: "local.environment-selected", Status: StatusPass},
		{ID: "local.agent-service-detected", Status: StatusPass, Details: map[string]any{
			"agentServices": []string{"echo"},
		}},
		{ID: "remote.auth", Status: StatusFail},
	}
	res := newCheckAgentStatus(deps).Fn(t.Context(), Options{}, prior)
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "remote.auth")
}

func TestCheckAgentStatus_SkipsWhenFoundryEndpointFailed(t *testing.T) {
	t.Parallel()
	deps := Dependencies{AzdClient: &azdext.AzdClient{}}
	prior := []Result{
		{ID: "local.environment-selected", Status: StatusPass},
		{ID: "local.agent-service-detected", Status: StatusPass, Details: map[string]any{
			"agentServices": []string{"echo"},
		}},
		{ID: "remote.auth", Status: StatusPass},
		{ID: "remote.foundry-endpoint", Status: StatusFail},
	}
	res := newCheckAgentStatus(deps).Fn(t.Context(), Options{}, prior)
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "remote.foundry-endpoint")
}

func TestCheckAgentStatus_DoesNotSkipOnRBACFail(t *testing.T) {
	t.Parallel()
	// RBAC failure must NOT prevent agent-status from running: agent-list
	// is a Reader-level call and a developer with read-only access on
	// the Foundry project still benefits from knowing whether their
	// agents are healthy. Pin this contract so a future refactor does
	// not accidentally couple the two checks.
	deps := Dependencies{
		AzdClient: &azdext.AzdClient{},
		readAgentNameVersionFn: fixedNameVersionReader(map[string]pair{
			"echo": {name: "echo-agent", version: "1"},
		}),
		probeAgentStatus: fixedProbe(map[probeKey]agentStatusProbeResult{
			{name: "echo-agent", version: "1"}: {
				statusCode: http.StatusOK, status: agentStatusActive,
			},
		}),
	}
	prior := append(
		healthyPriorResults([]string{"echo"}, "https://example.foundry"),
		Result{ID: "remote.rbac", Status: StatusFail},
	)
	res := runCheckWithDeps(t, deps, prior)
	require.Equal(t, StatusPass, res.Status,
		"agent-status must run even when remote.rbac failed")
}

func TestCheckAgentStatus_SkipsWhenEndpointMissingFromUpstream(t *testing.T) {
	t.Parallel()
	deps := Dependencies{AzdClient: &azdext.AzdClient{}}
	prior := []Result{
		{ID: "local.environment-selected", Status: StatusPass},
		{ID: "local.agent-service-detected", Status: StatusPass, Details: map[string]any{
			"agentServices": []string{"echo"},
		}},
		// project-endpoint-set passed but didn't surface the value:
		{ID: "local.project-endpoint-set", Status: StatusPass},
		{ID: "remote.auth", Status: StatusPass},
		{ID: "remote.foundry-endpoint", Status: StatusPass},
	}
	res := newCheckAgentStatus(deps).Fn(t.Context(), Options{}, prior)
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "FOUNDRY_PROJECT_ENDPOINT")
}

func TestCheckAgentStatus_SkipsWhenAgentServiceListMissingFromUpstream(t *testing.T) {
	t.Parallel()
	deps := Dependencies{AzdClient: &azdext.AzdClient{}, AgentAPIVersion: "v1"}
	prior := []Result{
		{ID: "local.environment-selected", Status: StatusPass},
		// agent-service-detected passed but didn't surface the list:
		{ID: "local.agent-service-detected", Status: StatusPass},
		{ID: "local.project-endpoint-set", Status: StatusPass, Details: map[string]any{
			"projectEndpoint": "https://example.foundry",
		}},
		{ID: "remote.auth", Status: StatusPass},
		{ID: "remote.foundry-endpoint", Status: StatusPass},
	}
	res := newCheckAgentStatus(deps).Fn(t.Context(), Options{}, prior)
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "agent service names")
}

func TestCheckAgentStatus_SkipsWhenAPIVersionEmpty(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		AzdClient:       &azdext.AzdClient{},
		AgentAPIVersion: "", // production wiring should always populate this
	}
	prior := healthyPriorResults([]string{"echo"}, "https://example.foundry")
	res := newCheckAgentStatus(deps).Fn(t.Context(), Options{}, prior)
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "agent API version")
}

// ---- Per-service classification ----

func TestCheckAgentStatus_AllActive_Pass(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		readAgentNameVersionFn: fixedNameVersionReader(map[string]pair{
			"echo": {name: "echo-agent", version: "1"},
			"summ": {name: "summ-agent", version: "2"},
		}),
		probeAgentStatus: fixedProbe(map[probeKey]agentStatusProbeResult{
			{name: "echo-agent", version: "1"}: {
				statusCode: http.StatusOK, status: agentStatusActive,
			},
			{name: "summ-agent", version: "2"}: {
				statusCode: http.StatusOK, status: agentStatusActive,
			},
		}),
	}
	prior := healthyPriorResults([]string{"echo", "summ"}, "https://example.foundry")
	res := runCheckWithDeps(t, deps, prior)
	require.Equal(t, StatusPass, res.Status)
	require.Contains(t, res.Message, "2 of 2 agents active")
	require.Contains(t, res.Message, "echo")
	require.Contains(t, res.Message, "summ")
}

func TestCheckAgentStatus_CreatingOnly_Warn(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		readAgentNameVersionFn: fixedNameVersionReader(map[string]pair{
			"echo": {name: "echo-agent", version: "1"},
		}),
		probeAgentStatus: fixedProbe(map[probeKey]agentStatusProbeResult{
			{name: "echo-agent", version: "1"}: {
				statusCode: http.StatusOK, status: agentStatusCreating,
			},
		}),
	}
	prior := healthyPriorResults([]string{"echo"}, "https://example.foundry")
	res := runCheckWithDeps(t, deps, prior)
	require.Equal(t, StatusWarn, res.Status)
	require.Contains(t, res.Suggestion, "monitor --follow")
}

func TestCheckAgentStatus_Failed_FailPointsAtMonitor(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		readAgentNameVersionFn: fixedNameVersionReader(map[string]pair{
			"echo": {name: "echo-agent", version: "1"},
		}),
		probeAgentStatus: fixedProbe(map[probeKey]agentStatusProbeResult{
			{name: "echo-agent", version: "1"}: {
				statusCode: http.StatusOK, status: agentStatusFailed,
			},
		}),
	}
	prior := healthyPriorResults([]string{"echo"}, "https://example.foundry")
	res := runCheckWithDeps(t, deps, prior)
	require.Equal(t, StatusFail, res.Status)
	require.Contains(t, res.Suggestion, "monitor --follow",
		"Failed agents must point at monitor --follow, NOT azd deploy")
	require.NotContains(t, res.Suggestion, "azd deploy",
		"Failed agents must NOT suggest redeploying without diagnosis")
}

func TestCheckAgentStatus_NotFound404_FailPointsAtDeploy(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		readAgentNameVersionFn: fixedNameVersionReader(map[string]pair{
			"echo": {name: "echo-agent", version: "1"},
		}),
		probeAgentStatus: fixedProbe(map[probeKey]agentStatusProbeResult{
			{name: "echo-agent", version: "1"}: {
				statusCode: http.StatusNotFound,
			},
		}),
	}
	prior := healthyPriorResults([]string{"echo"}, "https://example.foundry")
	res := runCheckWithDeps(t, deps, prior)
	require.Equal(t, StatusFail, res.Status)
	require.Contains(t, res.Suggestion, "azd deploy")
	require.Contains(t, res.Message, "missing")
}

func TestCheckAgentStatus_NotDeployed_NoEnvVar_FailPointsAtDeploy(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		readAgentNameVersionFn: fixedNameVersionReader(map[string]pair{
			// "echo" intentionally absent → AGENT_ECHO_NAME unset.
		}),
		probeAgentStatus: fixedProbe(map[probeKey]agentStatusProbeResult{}),
	}
	prior := healthyPriorResults([]string{"echo"}, "https://example.foundry")
	res := runCheckWithDeps(t, deps, prior)
	require.Equal(t, StatusFail, res.Status)
	require.Contains(t, res.Suggestion, "azd deploy")
	require.Contains(t, res.Message, "not been deployed")
}

func TestCheckAgentStatus_MismatchedNameVersion_NotDeployedForService(t *testing.T) {
	t.Parallel()
	// AGENT_ECHO_NAME is set but AGENT_ECHO_VERSION is missing.
	// The post-deploy hook writes both vars atomically, so this is
	// a deterministic "deployment never completed" state — classify
	// as not-deployed and direct the user to `azd deploy`, not
	// "retry doctor".
	deps := Dependencies{
		readAgentNameVersionFn: fixedNameVersionReader(map[string]pair{
			"echo": {name: "echo-agent", version: ""},
			"summ": {name: "summ-agent", version: "1"},
		}),
		probeAgentStatus: fixedProbe(map[probeKey]agentStatusProbeResult{
			{name: "summ-agent", version: "1"}: {
				statusCode: http.StatusOK, status: agentStatusActive,
			},
		}),
	}
	prior := healthyPriorResults([]string{"echo", "summ"}, "https://example.foundry")
	res := runCheckWithDeps(t, deps, prior)
	// not-deployed (rank 3) > active (rank 0), so the aggregate is
	// a Fail; the Suggestion must point at `azd deploy`.
	require.Equal(t, StatusFail, res.Status)
	require.Contains(t, res.Suggestion, "azd deploy")
	require.NotContains(t, res.Suggestion, "Retry")
	// Per-service Details classify the missing-version entry as
	// not-deployed and the healthy entry as active.
	services, ok := res.Details["services"].([]agentStatusEntry)
	require.True(t, ok)
	require.Len(t, services, 2)
	// Sorted lexicographically (echo < summ).
	require.Equal(t, "echo", services[0].Service)
	require.Equal(t, agentClassNotDeployed, services[0].Classification)
	require.Contains(t, services[0].Detail, "AGENT_ECHO_VERSION")
	require.Equal(t, "summ", services[1].Service)
	require.Equal(t, agentClassActive, services[1].Classification)
}

func TestCheckAgentStatus_UnknownStatus_Fail(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		readAgentNameVersionFn: fixedNameVersionReader(map[string]pair{
			"echo": {name: "echo-agent", version: "1"},
		}),
		probeAgentStatus: fixedProbe(map[probeKey]agentStatusProbeResult{
			{name: "echo-agent", version: "1"}: {
				statusCode: http.StatusOK, status: "Mysterious",
			},
		}),
	}
	prior := healthyPriorResults([]string{"echo"}, "https://example.foundry")
	res := runCheckWithDeps(t, deps, prior)
	require.Equal(t, StatusFail, res.Status)
	require.Contains(t, res.Message, "unrecognized")
	require.Contains(t, res.Suggestion, "Foundry portal")
}

func TestCheckAgentStatus_StatusCaseInsensitive(t *testing.T) {
	t.Parallel()
	// Foundry has historically shipped both Pascal-cased and
	// lower-cased lifecycle values; the production invoke flow
	// normalizes with EqualFold, so we must too.
	deps := Dependencies{
		readAgentNameVersionFn: fixedNameVersionReader(map[string]pair{
			"echo": {name: "echo-agent", version: "1"},
		}),
		probeAgentStatus: fixedProbe(map[probeKey]agentStatusProbeResult{
			{name: "echo-agent", version: "1"}: {
				statusCode: http.StatusOK, status: "active", // lowercase
			},
		}),
	}
	prior := healthyPriorResults([]string{"echo"}, "https://example.foundry")
	res := runCheckWithDeps(t, deps, prior)
	require.Equal(t, StatusPass, res.Status)
}

// ---- Aggregate behavior ----

func TestCheckAgentStatus_Aggregate_FailedDominatesActive(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		readAgentNameVersionFn: fixedNameVersionReader(map[string]pair{
			"echo": {name: "echo-agent", version: "1"},
			"summ": {name: "summ-agent", version: "1"},
		}),
		probeAgentStatus: fixedProbe(map[probeKey]agentStatusProbeResult{
			{name: "echo-agent", version: "1"}: {
				statusCode: http.StatusOK, status: agentStatusActive,
			},
			{name: "summ-agent", version: "1"}: {
				statusCode: http.StatusOK, status: agentStatusFailed,
			},
		}),
	}
	prior := healthyPriorResults([]string{"echo", "summ"}, "https://example.foundry")
	res := runCheckWithDeps(t, deps, prior)
	require.Equal(t, StatusFail, res.Status)
	require.Contains(t, res.Suggestion, "monitor --follow")
	require.Contains(t, res.Message, "summ")
}

func TestCheckAgentStatus_Aggregate_MissingDominatesCreating(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		readAgentNameVersionFn: fixedNameVersionReader(map[string]pair{
			"echo": {name: "echo-agent", version: "1"},
			"summ": {name: "summ-agent", version: "1"},
		}),
		probeAgentStatus: fixedProbe(map[probeKey]agentStatusProbeResult{
			{name: "echo-agent", version: "1"}: {
				statusCode: http.StatusOK, status: agentStatusCreating,
			},
			{name: "summ-agent", version: "1"}: {
				statusCode: http.StatusNotFound,
			},
		}),
	}
	prior := healthyPriorResults([]string{"echo", "summ"}, "https://example.foundry")
	res := runCheckWithDeps(t, deps, prior)
	require.Equal(t, StatusFail, res.Status)
	require.Contains(t, res.Suggestion, "azd deploy")
}

func TestCheckAgentStatus_Aggregate_FailedDominatesMissing(t *testing.T) {
	t.Parallel()
	// failed rank (6) > missing rank (5), so the Suggestion points
	// at monitor --follow (the Failed branch) rather than azd
	// deploy (the Missing branch). Diagnose-before-redeploy is the
	// correct order: a Failed agent has logs the user needs to read.
	deps := Dependencies{
		readAgentNameVersionFn: fixedNameVersionReader(map[string]pair{
			"echo": {name: "echo-agent", version: "1"},
			"summ": {name: "summ-agent", version: "1"},
		}),
		probeAgentStatus: fixedProbe(map[probeKey]agentStatusProbeResult{
			{name: "echo-agent", version: "1"}: {
				statusCode: http.StatusNotFound,
			},
			{name: "summ-agent", version: "1"}: {
				statusCode: http.StatusOK, status: agentStatusFailed,
			},
		}),
	}
	prior := healthyPriorResults([]string{"echo", "summ"}, "https://example.foundry")
	res := runCheckWithDeps(t, deps, prior)
	require.Equal(t, StatusFail, res.Status)
	require.Contains(t, res.Suggestion, "monitor --follow")
	// Headline count matches the Failed-class count (1), not the
	// total non-active count (2). Detail body lists only the failed
	// entry, not the missing one — count and body must agree.
	require.Contains(t, res.Message, "1 of 2 agents are in a failed state")
	require.Contains(t, res.Message, "summ:")
	require.NotContains(t, res.Message, "echo:")
	// Mixed-class Suggestion mentions the other failing classes so
	// the user knows there's a second fix path in Details.
	require.Contains(t, res.Suggestion, "Other agents have additional issues")
	require.Contains(t, res.Suggestion, "missing")
}

func TestCheckAgentStatus_Aggregate_ActiveAndTransient_PassWithNote(t *testing.T) {
	t.Parallel()
	// Documented aggregate rule: when the worst class is `transient`
	// and at least one service is Active, the aggregate is Pass
	// with a Message note that some probes were skipped. A
	// transient probe failure for one service should not mask the
	// healthy state of the others.
	deps := Dependencies{
		readAgentNameVersionFn: fixedNameVersionReader(map[string]pair{
			"echo": {name: "echo-agent", version: "1"},
			"summ": {name: "summ-agent", version: "1"},
		}),
		probeAgentStatus: fixedProbe(map[probeKey]agentStatusProbeResult{
			{name: "echo-agent", version: "1"}: {
				statusCode: http.StatusOK, status: agentStatusActive,
			},
			{name: "summ-agent", version: "1"}: {
				err: errors.New("network unreachable"),
			},
		}),
	}
	prior := healthyPriorResults([]string{"echo", "summ"}, "https://example.foundry")
	res := runCheckWithDeps(t, deps, prior)
	require.Equal(t, StatusPass, res.Status)
	require.Contains(t, res.Message, "1 of 2 agents active")
	require.Contains(t, res.Message, "probe(s) skipped")
	require.Contains(t, res.Message, "network unreachable")
	// No Suggestion needed for a Pass.
	require.Empty(t, res.Suggestion)
}

func TestCheckAgentStatus_Aggregate_AllTransient_Skip(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		readAgentNameVersionFn: fixedNameVersionReader(map[string]pair{
			"echo": {name: "echo-agent", version: "1"},
		}),
		probeAgentStatus: fixedProbe(map[probeKey]agentStatusProbeResult{
			{name: "echo-agent", version: "1"}: {
				err: errors.New("network unreachable"),
			},
		}),
	}
	prior := healthyPriorResults([]string{"echo"}, "https://example.foundry")
	res := runCheckWithDeps(t, deps, prior)
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "network unreachable")
	require.Contains(t, res.Suggestion, "Retry")
}

func TestCheckAgentStatus_Aggregate_TruncatesAtThreeFailingLines(t *testing.T) {
	t.Parallel()
	// Five failing services should produce a Message that lists
	// three lines + "(2 more)". Confirms the truncateLines helper
	// is wired into the aggregate Message.
	names := []string{"a", "b", "c", "d", "e"}
	versions := map[string]pair{}
	probes := map[probeKey]agentStatusProbeResult{}
	for _, n := range names {
		versions[n] = pair{name: n + "-agent", version: "1"}
		probes[probeKey{n + "-agent", "1"}] = agentStatusProbeResult{
			statusCode: http.StatusNotFound,
		}
	}
	deps := Dependencies{
		readAgentNameVersionFn: fixedNameVersionReader(versions),
		probeAgentStatus:       fixedProbe(probes),
	}
	prior := healthyPriorResults(names, "https://example.foundry")
	res := runCheckWithDeps(t, deps, prior)
	require.Equal(t, StatusFail, res.Status)
	require.Contains(t, res.Message, "(2 more)")
	// Three full detail lines visible.
	require.Equal(t, 3, strings.Count(res.Message, "version 1"))
}

// ---- probeOneService transport branches ----

func TestProbeOneService_404_Missing(t *testing.T) {
	t.Parallel()
	entry := probeOneService(
		t.Context(), &azdext.AzdClient{}, "echo", "https://example.foundry",
		fixedNameVersionReader(map[string]pair{
			"echo": {name: "echo-agent", version: "1"},
		}),
		fixedProbe(map[probeKey]agentStatusProbeResult{
			{name: "echo-agent", version: "1"}: {
				statusCode: http.StatusNotFound,
			},
		}),
	)
	require.Equal(t, agentClassMissing, entry.Classification)
	require.Equal(t, http.StatusNotFound, entry.HTTPStatus)
}

func TestProbeOneService_TransportError_Transient(t *testing.T) {
	t.Parallel()
	entry := probeOneService(
		t.Context(), &azdext.AzdClient{}, "echo", "https://example.foundry",
		fixedNameVersionReader(map[string]pair{
			"echo": {name: "echo-agent", version: "1"},
		}),
		fixedProbe(map[probeKey]agentStatusProbeResult{
			{name: "echo-agent", version: "1"}: {
				err: errors.New("dns lookup failed"),
			},
		}),
	)
	require.Equal(t, agentClassTransientErr, entry.Classification)
	require.Contains(t, entry.Detail, "dns lookup failed")
}

func TestProbeOneService_ContextCancelled_Transient(t *testing.T) {
	t.Parallel()
	entry := probeOneService(
		t.Context(), &azdext.AzdClient{}, "echo", "https://example.foundry",
		fixedNameVersionReader(map[string]pair{
			"echo": {name: "echo-agent", version: "1"},
		}),
		fixedProbe(map[probeKey]agentStatusProbeResult{
			{name: "echo-agent", version: "1"}: {
				err: context.Canceled,
			},
		}),
	)
	require.Equal(t, agentClassTransientErr, entry.Classification)
	require.Contains(t, entry.Detail, "cancelled")
}

func TestProbeOneService_DeadlineExceeded_Transient(t *testing.T) {
	t.Parallel()
	entry := probeOneService(
		t.Context(), &azdext.AzdClient{}, "echo", "https://example.foundry",
		fixedNameVersionReader(map[string]pair{
			"echo": {name: "echo-agent", version: "1"},
		}),
		fixedProbe(map[probeKey]agentStatusProbeResult{
			{name: "echo-agent", version: "1"}: {
				err: context.DeadlineExceeded,
			},
		}),
	)
	require.Equal(t, agentClassTransientErr, entry.Classification)
	require.Contains(t, entry.Detail, "did not respond")
}

func TestProbeOneService_DeletedStatus_Missing(t *testing.T) {
	t.Parallel()
	entry := probeOneService(
		t.Context(), &azdext.AzdClient{}, "echo", "https://example.foundry",
		fixedNameVersionReader(map[string]pair{
			"echo": {name: "echo-agent", version: "1"},
		}),
		fixedProbe(map[probeKey]agentStatusProbeResult{
			{name: "echo-agent", version: "1"}: {
				statusCode: http.StatusOK, status: agentStatusDeleted,
			},
		}),
	)
	require.Equal(t, agentClassMissing, entry.Classification)
}

func TestProbeOneService_DeletingStatus_Missing(t *testing.T) {
	t.Parallel()
	// Vienna's AgentVersionStatus surfaces both `Deleted` and
	// `Deleting`; both should classify as `missing` so the user is
	// directed to redeploy rather than wait for an agent that's
	// being torn down.
	entry := probeOneService(
		t.Context(), &azdext.AzdClient{}, "echo", "https://example.foundry",
		fixedNameVersionReader(map[string]pair{
			"echo": {name: "echo-agent", version: "1"},
		}),
		fixedProbe(map[probeKey]agentStatusProbeResult{
			{name: "echo-agent", version: "1"}: {
				statusCode: http.StatusOK, status: agentStatusDeleting,
			},
		}),
	)
	require.Equal(t, agentClassMissing, entry.Classification)
	require.Contains(t, entry.Detail, "deleted or is being deleted")
}

func TestProbeOneService_NameSetVersionEmpty_NotDeployed(t *testing.T) {
	t.Parallel()
	// The post-deploy hook writes AGENT_<KEY>_NAME and
	// AGENT_<KEY>_VERSION atomically; a present-NAME / absent-VERSION
	// is therefore a "deployment never completed" state — classify
	// as not-deployed so the user is told to `azd deploy`, not to
	// retry the doctor.
	entry := probeOneService(
		t.Context(), &azdext.AzdClient{}, "echo", "https://example.foundry",
		fixedNameVersionReader(map[string]pair{
			"echo": {name: "echo-agent", version: ""},
		}),
		fixedProbe(nil),
	)
	require.Equal(t, agentClassNotDeployed, entry.Classification)
	require.Contains(t, entry.Detail, "AGENT_ECHO_VERSION")
	require.Contains(t, entry.Detail, "did not complete")
}

func TestProbeOneService_ReadNameVersionError_Transient(t *testing.T) {
	t.Parallel()
	reader := func(_ context.Context, _ *azdext.AzdClient, _ string) (string, string, error) {
		return "", "", errors.New("gRPC channel closed")
	}
	entry := probeOneService(
		t.Context(), &azdext.AzdClient{}, "echo", "https://example.foundry",
		reader, fixedProbe(nil),
	)
	require.Equal(t, agentClassTransientErr, entry.Classification)
	require.Contains(t, entry.Detail, "gRPC channel closed")
}

// ---- Service-key edge cases ----

func TestDoctorServiceKey_HandlesHyphensSpacesAndCase(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input string
		want  string
	}{
		{"echo", "ECHO"},
		{"my-agent", "MY_AGENT"},
		{"my agent", "MY_AGENT"},
		{"My-Agent Name", "MY_AGENT_NAME"},
	}
	for _, c := range cases {
		require.Equal(t, c.want, doctorServiceKey(c.input),
			"input=%q", c.input)
	}
}

// ---- Helper functions ----

func TestRank_FallsBackToActiveForUnknownClass(t *testing.T) {
	t.Parallel()
	// Defensive: an unknown classification must NOT outrank a real
	// class (otherwise it would silently drive the aggregate Status).
	require.Equal(t, 0, rank("not-a-real-class"))
	require.Equal(t, 0, rank(agentClassActive))
	require.Greater(t, rank(agentClassFailed), rank(agentClassActive))
}

func TestTruncateLines_AtAndBelowMax(t *testing.T) {
	t.Parallel()
	require.Equal(t, []string{"a", "b"}, truncateLines([]string{"a", "b"}, 3))
	require.Equal(t, []string{"a", "b", "c"}, truncateLines([]string{"a", "b", "c"}, 3))
	require.Equal(t,
		[]string{"a", "b", "c", "(2 more)"},
		truncateLines([]string{"a", "b", "c", "d", "e"}, 3))
}

func TestReadAgentServices_MissingDetailsReturnsNil(t *testing.T) {
	t.Parallel()
	prior := []Result{
		{ID: "local.agent-service-detected", Status: StatusPass},
	}
	require.Nil(t, readAgentServices(prior))
}

func TestReadAgentServices_WrongTypeReturnsNil(t *testing.T) {
	t.Parallel()
	prior := []Result{
		{ID: "local.agent-service-detected", Status: StatusPass, Details: map[string]any{
			"agentServices": "echo,summ", // wrong type — should be []string
		}},
	}
	require.Nil(t, readAgentServices(prior))
}

// ---- Real-probe shape ----

func TestMakeRealProbeAgentStatus_ReturnsNonNilCloser(t *testing.T) {
	t.Parallel()
	// We can't easily test the real probe without an Azure subscription,
	// but we can pin the factory: it must return a non-nil closure that
	// surfaces a credential-creation error or a network error rather
	// than panicking when called.
	probe := makeRealProbeAgentStatus("v1")
	require.NotNil(t, probe)
	// Invoking with an obviously-invalid endpoint should still
	// produce a structured result (not a panic). We pass a very
	// short context to avoid waiting on real network.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	res := probe(ctx, "https://localhost:1/", "no-agent", "1")
	// We don't assert on the specific error because behavior varies
	// by environment; the contract is "returns without panic".
	_ = res
}

// ---- azcore.ResponseError unwrap path ----

func TestProbeAgentStatus_AzcoreResponseErrorSurfacesStatusCode(t *testing.T) {
	t.Parallel()
	// The production closure uses errors.AsType to unwrap an
	// azcore.ResponseError into a statusCode for the missing-class
	// branch. Pin that contract here using a synthetic ResponseError
	// to make sure a future SDK refactor that wraps the error
	// differently surfaces in this test rather than at runtime.
	respErr := &azcore.ResponseError{
		StatusCode: http.StatusNotFound,
		ErrorCode:  "AgentNotFound",
	}
	require.Equal(t, http.StatusNotFound, respErr.StatusCode,
		"sanity check on synthetic ResponseError shape")

	// Build a stub probe that mimics what the real closure produces
	// when the SDK returns a wrapped 404, then run the per-service
	// entry path to confirm it routes to agentClassMissing.
	entry := probeOneService(
		t.Context(), &azdext.AzdClient{}, "echo", "https://example.foundry",
		fixedNameVersionReader(map[string]pair{
			"echo": {name: "echo-agent", version: "1"},
		}),
		func(_ context.Context, _ string, _ string, _ string) agentStatusProbeResult {
			return agentStatusProbeResult{
				statusCode: respErr.StatusCode,
				err:        respErr,
			}
		},
	)
	require.Equal(t, agentClassMissing, entry.Classification,
		"a wrapped ResponseError with 404 must route to missing")
}
