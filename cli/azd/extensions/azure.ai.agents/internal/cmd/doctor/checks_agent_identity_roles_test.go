// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

// agentIdentityPriorResults produces a complete prior-result slice
// satisfying every skip-cascade gate `remote.agent-identity-roles`
// declares. The `agentStatusEntries` slice is the production-shape
// listing the upstream `remote.agent-status` check surfaces under
// `Details["services"]`; only entries with Classification == "active"
// will be consumed by C12.
func agentIdentityPriorResults(
	agentStatusEntries []agentStatusEntry,
	endpoint string,
) []Result {
	return []Result{
		{ID: "local.environment-selected", Status: StatusPass},
		{ID: "local.agent-service-detected", Status: StatusPass},
		{ID: "local.project-endpoint-set", Status: StatusPass, Details: map[string]any{
			"projectEndpoint": endpoint,
		}},
		{ID: "remote.auth", Status: StatusPass},
		{ID: "remote.foundry-endpoint", Status: StatusPass},
		{ID: "remote.agent-status", Status: StatusPass, Details: map[string]any{
			"services": agentStatusEntries,
		}},
	}
}

// runIdentityCheck wires deps with default test-friendly values and
// invokes the check. Tests that need to override a seam pass it in
// `deps`; defaults preserve the no-network contract.
func runIdentityCheck(t *testing.T, deps Dependencies, prior []Result) Result {
	t.Helper()
	if deps.AzdClient == nil {
		deps.AzdClient = &azdext.AzdClient{}
	}
	if deps.AgentAPIVersion == "" {
		deps.AgentAPIVersion = "2025-11-15-preview"
	}
	if deps.readProjectResourceIDFn == nil {
		deps.readProjectResourceIDFn = func(_ context.Context, _ *azdext.AzdClient) (string, error) {
			return "/subscriptions/sub-1/resourceGroups/rg-1/providers/" +
				"Microsoft.CognitiveServices/accounts/acc-1/projects/proj-1", nil
		}
	}
	if deps.probeAgentPrincipal == nil {
		// Default: every probe returns a deterministic principal ID
		// derived from the agent name. Tests overriding need do so
		// explicitly via deps.probeAgentPrincipal.
		deps.probeAgentPrincipal = func(_ context.Context, _, name, _ string) agentIdentityProbeResult {
			return agentIdentityProbeResult{
				StatusCode:  200,
				PrincipalID: "principal-" + name,
			}
		}
	}
	c := newCheckAgentIdentityRoles(deps)
	require.NotNil(t, c.Fn, "newCheckAgentIdentityRoles must return a non-nil Fn")
	return c.Fn(t.Context(), Options{}, prior)
}

// ---- Skip-cascade gates ----

func TestCheckAgentIdentityRoles_SkipsWhenAzdClientNil(t *testing.T) {
	t.Parallel()
	c := newCheckAgentIdentityRoles(Dependencies{})
	res := c.Fn(t.Context(), Options{}, nil)
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "azd extension not reachable")
}

func TestCheckAgentIdentityRoles_SkipsWhenAgentStatusNotPassed(t *testing.T) {
	t.Parallel()
	prior := []Result{
		{ID: "local.environment-selected", Status: StatusPass},
		{ID: "local.agent-service-detected", Status: StatusPass},
		{ID: "remote.auth", Status: StatusPass},
		{ID: "remote.foundry-endpoint", Status: StatusPass},
		{ID: "remote.agent-status", Status: StatusFail},
	}
	res := runIdentityCheck(t, Dependencies{}, prior)
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "remote.agent-status")
}

func TestCheckAgentIdentityRoles_SkipsWhenProjectEndpointMissing(t *testing.T) {
	t.Parallel()
	// Drop the project-endpoint Result's Details
	prior := agentIdentityPriorResults(
		[]agentStatusEntry{{Service: "svc", AgentName: "a", AgentVersion: "1", Classification: agentClassActive}},
		"")
	res := runIdentityCheck(t, Dependencies{}, prior)
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "FOUNDRY_PROJECT_ENDPOINT")
}

func TestCheckAgentIdentityRoles_SkipsWhenNoActiveAgents(t *testing.T) {
	t.Parallel()
	// Only Creating / Failed entries — no active ones.
	prior := agentIdentityPriorResults(
		[]agentStatusEntry{
			{Service: "a", AgentName: "an", AgentVersion: "1", Classification: agentClassDeploying},
			{Service: "b", AgentName: "bn", AgentVersion: "1", Classification: agentClassFailed},
		},
		"https://example.local")
	res := runIdentityCheck(t, Dependencies{}, prior)
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "active agents")
}

func TestCheckAgentIdentityRoles_SkipsWhenAPIVersionEmpty(t *testing.T) {
	t.Parallel()
	// Bypass runIdentityCheck so AgentAPIVersion stays empty —
	// runIdentityCheck would auto-populate it. Build deps with the
	// minimum needed to clear the AzdClient nil guard.
	deps := Dependencies{
		AzdClient: &azdext.AzdClient{},
		// AgentAPIVersion deliberately empty
		readProjectResourceIDFn: func(_ context.Context, _ *azdext.AzdClient) (string, error) {
			return "/subscriptions/sub-1/resourceGroups/rg-1/providers/" +
				"Microsoft.CognitiveServices/accounts/acc-1/projects/proj-1", nil
		},
	}
	prior := agentIdentityPriorResults(
		[]agentStatusEntry{{Service: "svc", AgentName: "a", AgentVersion: "1", Classification: agentClassActive}},
		"https://example.local")
	res := newCheckAgentIdentityRoles(deps).Fn(t.Context(), Options{}, prior)
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "agent API version")
}

func TestCheckAgentIdentityRoles_SkipsWhenProjectResourceIDUnset(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		readProjectResourceIDFn: func(_ context.Context, _ *azdext.AzdClient) (string, error) {
			return "", nil
		},
	}
	prior := agentIdentityPriorResults(
		[]agentStatusEntry{{Service: "svc", AgentName: "a", AgentVersion: "1", Classification: agentClassActive}},
		"https://example.local")
	res := runIdentityCheck(t, deps, prior)
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "AZURE_AI_PROJECT_ID")
}

func TestCheckAgentIdentityRoles_SkipsWhenProjectResourceIDMalformed(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		queryAgentIdentityRoles: func(_ context.Context, _ *azdext.AzdClient, _ string, _ []project.AgentPrincipal) (*project.AgentIdentityRolesResult, error) {
			return nil, project.ErrInvalidProjectResourceID
		},
	}
	prior := agentIdentityPriorResults(
		[]agentStatusEntry{{Service: "svc", AgentName: "a", AgentVersion: "1", Classification: agentClassActive}},
		"https://example.local")
	res := runIdentityCheck(t, deps, prior)
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "malformed")
}

// ---- Aggregate classification ----

func makeQueryReturning(result *project.AgentIdentityRolesResult) func(
	context.Context, *azdext.AzdClient, string, []project.AgentPrincipal,
) (*project.AgentIdentityRolesResult, error) {
	return func(_ context.Context, _ *azdext.AzdClient, _ string, principals []project.AgentPrincipal) (*project.AgentIdentityRolesResult, error) {
		// Echo the input principals' agent names through to entries
		// when the test supplied a single-entry result keyed by name;
		// otherwise return the canned result verbatim.
		_ = principals
		return result, nil
	}
}

func TestCheckAgentIdentityRoles_AggregateInfoWhenAllAgentsFine(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		queryAgentIdentityRoles: makeQueryReturning(&project.AgentIdentityRolesResult{
			Entries: []project.AgentIdentityRolesEntry{
				{
					AgentName:    "a",
					PrincipalID:  "principal-a",
					ProjectScope: project.AgentScopeRoles{Scope: "project", Roles: []string{"Azure AI User"}},
					AccountScope: project.AgentScopeRoles{Scope: "account", Roles: []string{"Cognitive Services User"}},
					RGScope:      project.AgentScopeRoles{Scope: "resource-group", Roles: []string{}},
				},
			},
			Scopes: project.AgentIdentityScopes{Project: "scope-p", Account: "scope-a", ResourceGroup: "scope-rg"},
		}),
	}
	prior := agentIdentityPriorResults(
		[]agentStatusEntry{{Service: "svc-a", AgentName: "a", AgentVersion: "1", Classification: agentClassActive}},
		"https://example.local")
	res := runIdentityCheck(t, deps, prior)
	require.Equal(t, StatusInfo, res.Status)
	require.Contains(t, res.Message, "1 of 1 agents")
	require.Contains(t, res.Suggestion, "no action needed")
}

func TestCheckAgentIdentityRoles_AggregateFailWhenAnyAgentEmpty(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		queryAgentIdentityRoles: makeQueryReturning(&project.AgentIdentityRolesResult{
			Entries: []project.AgentIdentityRolesEntry{
				{
					AgentName:    "a",
					PrincipalID:  "principal-a",
					ProjectScope: project.AgentScopeRoles{Scope: "project", Roles: []string{"Azure AI User"}},
					AccountScope: project.AgentScopeRoles{Scope: "account", Roles: []string{}},
					RGScope:      project.AgentScopeRoles{Scope: "resource-group", Roles: []string{}},
				},
				{
					AgentName:    "b",
					PrincipalID:  "principal-b",
					ProjectScope: project.AgentScopeRoles{Scope: "project", Roles: []string{}},
					AccountScope: project.AgentScopeRoles{Scope: "account", Roles: []string{}},
					RGScope:      project.AgentScopeRoles{Scope: "resource-group", Roles: []string{}},
				},
			},
		}),
	}
	prior := agentIdentityPriorResults(
		[]agentStatusEntry{
			{Service: "svc-a", AgentName: "a", AgentVersion: "1", Classification: agentClassActive},
			{Service: "svc-b", AgentName: "b", AgentVersion: "1", Classification: agentClassActive},
		},
		"https://example.local")
	res := runIdentityCheck(t, deps, prior)
	require.Equal(t, StatusFail, res.Status)
	require.Contains(t, res.Message, "zero role assignments")
	require.Contains(t, res.Suggestion, "az role assignment create")
}

func TestCheckAgentIdentityRoles_AggregateWarnWhenAgentUnderscoped(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		queryAgentIdentityRoles: makeQueryReturning(&project.AgentIdentityRolesResult{
			Entries: []project.AgentIdentityRolesEntry{
				{
					AgentName:   "a",
					PrincipalID: "principal-a",
					// project covered but neither account nor RG — underscoped
					ProjectScope: project.AgentScopeRoles{Scope: "project", Roles: []string{"Azure AI User"}},
					AccountScope: project.AgentScopeRoles{Scope: "account", Roles: []string{}},
					RGScope:      project.AgentScopeRoles{Scope: "resource-group", Roles: []string{}},
				},
			},
		}),
	}
	prior := agentIdentityPriorResults(
		[]agentStatusEntry{{Service: "svc-a", AgentName: "a", AgentVersion: "1", Classification: agentClassActive}},
		"https://example.local")
	res := runIdentityCheck(t, deps, prior)
	require.Equal(t, StatusWarn, res.Status)
	require.Contains(t, res.Message, "under-privileged")
}

func TestCheckAgentIdentityRoles_AggregateWarnOnQueryError(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		queryAgentIdentityRoles: func(_ context.Context, _ *azdext.AzdClient, _ string, _ []project.AgentPrincipal) (*project.AgentIdentityRolesResult, error) {
			return nil, errors.New("ARM transient")
		},
	}
	prior := agentIdentityPriorResults(
		[]agentStatusEntry{{Service: "svc-a", AgentName: "a", AgentVersion: "1", Classification: agentClassActive}},
		"https://example.local")
	res := runIdentityCheck(t, deps, prior)
	require.Equal(t, StatusWarn, res.Status)
	require.Contains(t, res.Message, "ARM transient")
}

// ---- Per-agent classifier ----

func TestClassifyOneAgent_FineWhenProjectPlusAccountOrRG(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		qe   project.AgentIdentityRolesEntry
		want string
	}{
		{
			name: "project+account → fine",
			qe: project.AgentIdentityRolesEntry{
				ProjectScope: project.AgentScopeRoles{Roles: []string{"r"}},
				AccountScope: project.AgentScopeRoles{Roles: []string{"r"}},
				RGScope:      project.AgentScopeRoles{Roles: []string{}},
			},
			want: agentIdentityClassFine,
		},
		{
			name: "project+RG → fine",
			qe: project.AgentIdentityRolesEntry{
				ProjectScope: project.AgentScopeRoles{Roles: []string{"r"}},
				AccountScope: project.AgentScopeRoles{Roles: []string{}},
				RGScope:      project.AgentScopeRoles{Roles: []string{"r"}},
			},
			want: agentIdentityClassFine,
		},
		{
			name: "project only → underscoped",
			qe: project.AgentIdentityRolesEntry{
				ProjectScope: project.AgentScopeRoles{Roles: []string{"r"}},
				AccountScope: project.AgentScopeRoles{Roles: []string{}},
				RGScope:      project.AgentScopeRoles{Roles: []string{}},
			},
			want: agentIdentityClassUnderscoped,
		},
		{
			name: "account only → underscoped (no project coverage)",
			qe: project.AgentIdentityRolesEntry{
				ProjectScope: project.AgentScopeRoles{Roles: []string{}},
				AccountScope: project.AgentScopeRoles{Roles: []string{"r"}},
				RGScope:      project.AgentScopeRoles{Roles: []string{}},
			},
			want: agentIdentityClassUnderscoped,
		},
		{
			name: "all empty → empty",
			qe: project.AgentIdentityRolesEntry{
				ProjectScope: project.AgentScopeRoles{Roles: []string{}},
				AccountScope: project.AgentScopeRoles{Roles: []string{}},
				RGScope:      project.AgentScopeRoles{Roles: []string{}},
			},
			want: agentIdentityClassEmpty,
		},
		{
			name: "all errored → unknown",
			qe: project.AgentIdentityRolesEntry{
				ProjectScope: project.AgentScopeRoles{Err: errors.New("e")},
				AccountScope: project.AgentScopeRoles{Err: errors.New("e")},
				RGScope:      project.AgentScopeRoles{Err: errors.New("e")},
			},
			want: agentIdentityClassUnknown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyOneAgent(tc.qe)
			require.Equal(t, tc.want, got)
		})
	}
}

// ---- Detail formatting ----

func TestDescribeOneAgent_RendersScopeCounts(t *testing.T) {
	t.Parallel()
	qe := project.AgentIdentityRolesEntry{
		AgentName:    "agent-x",
		ProjectScope: project.AgentScopeRoles{Roles: []string{"a", "b"}},
		AccountScope: project.AgentScopeRoles{Roles: []string{}},
		RGScope:      project.AgentScopeRoles{Err: errors.New("listing failed")},
	}
	got := describeOneAgent(qe)
	require.Equal(t, "agent-x: project=2, account=0, resource-group=?", got)
}

// ---- Redaction ----

// TestCheckAgentIdentityRoles_RedactedDetailsDoNotLeakIdentifiers
// asserts the doctor's redaction contract: when Options.Unredacted
// is false (the default), Details must not surface raw principal IDs,
// raw ARM scope ARNs, or scope-bearing error strings. With
// Unredacted=true, the same identifiers must pass through verbatim
// so operators running `--unredacted` see what the backend returned.
func TestCheckAgentIdentityRoles_RedactedDetailsDoNotLeakIdentifiers(t *testing.T) {
	t.Parallel()
	rawPrincipal := "11111111-2222-3333-4444-555555555555"
	rawProjectScope := "/subscriptions/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/" +
		"resourceGroups/rg-secret/providers/Microsoft.CognitiveServices/" +
		"accounts/acc-secret/projects/proj-secret"
	rawAccountScope := "/subscriptions/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/" +
		"resourceGroups/rg-secret/providers/Microsoft.CognitiveServices/" +
		"accounts/acc-secret"
	rawRGScope := "/subscriptions/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/" +
		"resourceGroups/rg-secret"
	rawScopeBearingErr := errors.New("failed to list role assignments at " +
		"scope " + rawProjectScope + ": forbidden")
	canned := &project.AgentIdentityRolesResult{
		Entries: []project.AgentIdentityRolesEntry{
			{
				AgentName:    "a",
				PrincipalID:  rawPrincipal,
				ProjectScope: project.AgentScopeRoles{Scope: "project", Err: rawScopeBearingErr},
				AccountScope: project.AgentScopeRoles{Scope: "account", Roles: []string{"r"}},
				RGScope:      project.AgentScopeRoles{Scope: "resource-group", Roles: []string{"r"}},
			},
		},
		Scopes: project.AgentIdentityScopes{
			Project:       rawProjectScope,
			Account:       rawAccountScope,
			ResourceGroup: rawRGScope,
		},
	}
	deps := Dependencies{
		queryAgentIdentityRoles: makeQueryReturning(canned),
	}
	prior := agentIdentityPriorResults(
		[]agentStatusEntry{{Service: "svc-a", AgentName: "a", AgentVersion: "1", Classification: agentClassActive}},
		"https://example.local")

	// Redacted (default).
	res := runIdentityCheck(t, deps, prior)
	// Serialize Details to a single string so the assertion catches
	// leaks regardless of struct key path.
	detailsRedacted := flattenDetails(res.Details)
	require.NotContains(t, detailsRedacted, rawPrincipal,
		"redacted Details must not contain raw principal ID")
	require.NotContains(t, detailsRedacted, rawProjectScope,
		"redacted Details must not contain raw project scope ARN")
	require.NotContains(t, detailsRedacted, rawAccountScope,
		"redacted Details must not contain raw account scope ARN")
	require.NotContains(t, detailsRedacted, rawRGScope,
		"redacted Details must not contain raw RG scope ARN")
	require.Contains(t, detailsRedacted, "<redacted>",
		"redacted Details must contain the redacted placeholder")

	// Unredacted: same fixture, opts.Unredacted=true.
	check := newCheckAgentIdentityRoles(Dependencies{
		AzdClient: &azdext.AzdClient{},
		readProjectResourceIDFn: func(_ context.Context, _ *azdext.AzdClient) (string, error) {
			return "/subscriptions/sub-1/resourceGroups/rg-1/providers/" +
				"Microsoft.CognitiveServices/accounts/acc-1/projects/proj-1", nil
		},
		probeAgentPrincipal: func(_ context.Context, _, _, _ string) agentIdentityProbeResult {
			return agentIdentityProbeResult{PrincipalID: rawPrincipal, StatusCode: 200}
		},
		queryAgentIdentityRoles: makeQueryReturning(canned),
		AgentAPIVersion:         "2025-11-15-preview",
	})
	resU := check.Fn(t.Context(), Options{Unredacted: true}, prior)
	detailsUnredacted := flattenDetails(resU.Details)
	require.Contains(t, detailsUnredacted, rawPrincipal,
		"unredacted Details must contain raw principal ID")
	require.Contains(t, detailsUnredacted, rawProjectScope,
		"unredacted Details must contain raw project scope ARN")
}

// flattenDetails walks the Details map and returns a single string
// suitable for substring assertions. Used by redaction tests so
// callers don't need to know the exact key path the check emits.
func flattenDetails(d map[string]any) string {
	if d == nil {
		return ""
	}
	var sb strings.Builder
	for k, v := range d {
		sb.WriteString(k)
		sb.WriteString("=")
		sb.WriteString(stringify(v))
		sb.WriteString("\n")
	}
	return sb.String()
}

func stringify(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]string:
		var sb strings.Builder
		for k, val := range t {
			sb.WriteString(k)
			sb.WriteString("=")
			sb.WriteString(val)
			sb.WriteString(";")
		}
		return sb.String()
	case []agentIdentityRoleEntry:
		var sb strings.Builder
		for _, e := range t {
			sb.WriteString(e.AgentName)
			sb.WriteString(":")
			sb.WriteString(e.PrincipalID)
			sb.WriteString(";")
			sb.WriteString(e.ProjectErr)
			sb.WriteString(";")
			sb.WriteString(e.AccountErr)
			sb.WriteString(";")
			sb.WriteString(e.RGErr)
			sb.WriteString("|")
		}
		return sb.String()
	default:
		return ""
	}
}

// ---- Missing-principal degradation ----

func TestCheckAgentIdentityRoles_DegradesWhenPrincipalMissing(t *testing.T) {
	t.Parallel()
	// Build a Result where principal probe is "missing"; the query
	// fake will surface that as an unknown-class entry.
	deps := Dependencies{
		probeAgentPrincipal: func(_ context.Context, _, _, _ string) agentIdentityProbeResult {
			return agentIdentityProbeResult{Err: errors.New("no identity")}
		},
		queryAgentIdentityRoles: func(_ context.Context, _ *azdext.AzdClient, _ string, principals []project.AgentPrincipal) (*project.AgentIdentityRolesResult, error) {
			// Echo missing-principal entries with all-error scopes
			// to mirror what production QueryAgentIdentityRoles
			// produces when PrincipalID == "".
			entries := make([]project.AgentIdentityRolesEntry, 0, len(principals))
			for _, p := range principals {
				entries = append(entries, project.AgentIdentityRolesEntry{
					AgentName:    p.AgentName,
					PrincipalID:  "",
					ProjectScope: project.AgentScopeRoles{Err: errors.New("principal ID unavailable")},
					AccountScope: project.AgentScopeRoles{Err: errors.New("principal ID unavailable")},
					RGScope:      project.AgentScopeRoles{Err: errors.New("principal ID unavailable")},
				})
			}
			return &project.AgentIdentityRolesResult{Entries: entries}, nil
		},
	}
	prior := agentIdentityPriorResults(
		[]agentStatusEntry{{Service: "svc-a", AgentName: "a", AgentVersion: "1", Classification: agentClassActive}},
		"https://example.local")
	res := runIdentityCheck(t, deps, prior)
	require.Equal(t, StatusWarn, res.Status)
	require.True(t, strings.Contains(res.Message, "could not list") || strings.Contains(res.Message, "transient"))
}

// ---- readActiveAgents filtering ----

func TestReadActiveAgents_FiltersToActiveOnly(t *testing.T) {
	t.Parallel()
	prior := []Result{
		{ID: "remote.agent-status", Status: StatusPass, Details: map[string]any{
			"services": []agentStatusEntry{
				{Service: "a", AgentName: "an", AgentVersion: "1", Classification: agentClassActive},
				{Service: "b", AgentName: "bn", AgentVersion: "1", Classification: agentClassFailed},
				{Service: "c", AgentName: "cn", AgentVersion: "1", Classification: agentClassDeploying},
				{Service: "d", AgentName: "", AgentVersion: "1", Classification: agentClassActive}, // missing name dropped
			},
		}},
	}
	got := readActiveAgents(prior)
	require.Len(t, got, 1)
	require.Equal(t, "an", got[0].AgentName)
}

func TestReadActiveAgents_ReturnsNilWhenAgentStatusMissing(t *testing.T) {
	t.Parallel()
	got := readActiveAgents(nil)
	require.Nil(t, got)
}
