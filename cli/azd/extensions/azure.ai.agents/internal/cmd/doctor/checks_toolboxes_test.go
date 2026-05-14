// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"errors"
	"testing"

	"azureaiagent/internal/cmd/nextstep"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

// fixedToolboxLookup returns a toolboxEnvLookupFn that resolves a
// canned (value, ok) map keyed by canonical env var name. Unknown
// keys return ("", nil) — matching the azd env-service contract for
// missing keys (see makeRealToolboxEnvLookup's doc comment).
func fixedToolboxLookup(values map[string]string) toolboxEnvLookupFn {
	return func(_ context.Context, key string) (string, error) {
		return values[key], nil
	}
}

func runToolboxesCheck(t *testing.T, deps Dependencies, prior []Result) Result {
	t.Helper()
	if deps.AzdClient == nil {
		deps.AzdClient = &azdext.AzdClient{}
	}
	c := newCheckToolboxes(deps)
	require.NotNil(t, c.Fn)
	require.Equal(t, "local.toolboxes", c.ID)
	require.False(t, c.Remote, "toolboxes check must be tagged local (Remote=false)")
	return c.Fn(t.Context(), Options{}, prior)
}

// stateWithToolboxes builds a *nextstep.State whose HasToolboxes flag
// is wired to match the supplied slice (mirrors what the C2 manifest
// walker would produce).
func stateWithToolboxes(refs ...nextstep.ResourceRef) *nextstep.State {
	return &nextstep.State{
		HasToolboxes: len(refs) > 0,
		Toolboxes:    refs,
	}
}

// ---- Skip-cascade gates ----

func TestCheckToolboxes_SkipsWhenAzdClientNil(t *testing.T) {
	t.Parallel()
	c := newCheckToolboxes(Dependencies{})
	res := c.Fn(t.Context(), Options{}, nil)
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "azd extension not reachable")
}

func TestCheckToolboxes_SkipsCascadeFromUpstream(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		failedID string
	}{
		{"environment selected blocked", "local.environment-selected"},
		{"azure.yaml blocked", "local.azure-yaml"},
		{"agent service detected blocked", "local.agent-service-detected"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			prior := []Result{{ID: tc.failedID, Status: StatusFail}}
			deps := Dependencies{
				AzdClient:     &azdext.AzdClient{},
				assembleState: fixedAssembler(stateWithToolboxes(nextstep.ResourceRef{Name: "wst"})),
				lookupToolboxEnv: fixedToolboxLookup(map[string]string{
					"TOOLBOX_WST_MCP_ENDPOINT": "https://example/mcp",
				}),
			}
			res := runToolboxesCheck(t, deps, prior)
			require.Equal(t, StatusSkip, res.Status)
			require.Contains(t, res.Message, tc.failedID)
		})
	}
}

// Toolboxes is NOT gated on remote.auth / remote.foundry-endpoint —
// it only reads local env state. A failed auth/foundry-endpoint must
// not poison the toolbox check.
func TestCheckToolboxes_NotGatedOnRemotePriors(t *testing.T) {
	t.Parallel()
	prior := []Result{
		{ID: "local.azure-yaml", Status: StatusPass},
		{ID: "local.environment-selected", Status: StatusPass},
		{ID: "local.agent-service-detected", Status: StatusPass},
		{ID: "remote.auth", Status: StatusFail},
		{ID: "remote.foundry-endpoint", Status: StatusFail},
	}
	deps := Dependencies{
		AzdClient:     &azdext.AzdClient{},
		assembleState: fixedAssembler(stateWithToolboxes(nextstep.ResourceRef{Name: "wst"})),
		lookupToolboxEnv: fixedToolboxLookup(map[string]string{
			"TOOLBOX_WST_MCP_ENDPOINT": "https://example/mcp",
		}),
	}
	res := runToolboxesCheck(t, deps, prior)
	require.Equal(t, StatusPass, res.Status)
}

// ---- State emptiness ----

func TestCheckToolboxes_SkipsWhenNoToolboxesDeclared(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		AzdClient:     &azdext.AzdClient{},
		assembleState: fixedAssembler(&nextstep.State{}),
	}
	res := runToolboxesCheck(t, deps, nil)
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "no toolbox resources")
}

func TestCheckToolboxes_SkipsWhenAssemblerReturnsNil(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		AzdClient:     &azdext.AzdClient{},
		assembleState: fixedAssembler(nil),
	}
	res := runToolboxesCheck(t, deps, nil)
	require.Equal(t, StatusSkip, res.Status)
}

// ---- Classification: all-present / partial / all-missing ----

func TestCheckToolboxes_PassesWhenAllEndpointsSet(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		AzdClient: &azdext.AzdClient{},
		assembleState: fixedAssembler(stateWithToolboxes(
			nextstep.ResourceRef{Name: "web-search-tools", ServiceName: "svc-a"},
			nextstep.ResourceRef{Name: "code-runner", ServiceName: "svc-b"},
		)),
		lookupToolboxEnv: fixedToolboxLookup(map[string]string{
			"TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT": "https://wst.example/mcp",
			"TOOLBOX_CODE_RUNNER_MCP_ENDPOINT":      "https://cr.example/mcp",
		}),
	}
	res := runToolboxesCheck(t, deps, nil)
	require.Equal(t, StatusPass, res.Status)
	require.Contains(t, res.Message, "2 declared toolbox(es)")
	require.Equal(t, 2, res.Details["matchedCount"])
}

func TestCheckToolboxes_FailsWhenSomeEndpointsMissing(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		AzdClient: &azdext.AzdClient{},
		assembleState: fixedAssembler(stateWithToolboxes(
			nextstep.ResourceRef{Name: "web-search-tools", ServiceName: "svc-a"},
			nextstep.ResourceRef{Name: "code-runner", ServiceName: "svc-b"},
		)),
		lookupToolboxEnv: fixedToolboxLookup(map[string]string{
			"TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT": "https://wst.example/mcp",
			// code-runner missing
		}),
	}
	res := runToolboxesCheck(t, deps, nil)
	require.Equal(t, StatusFail, res.Status)
	require.Contains(t, res.Message, "code-runner")
	require.Contains(t, res.Message, "TOOLBOX_CODE_RUNNER_MCP_ENDPOINT")
	require.NotContains(t, res.Message, "web-search-tools")
	require.Contains(t, res.Suggestion, "azd provision")
	require.Equal(t, 1, res.Details["matchedCount"])
}

func TestCheckToolboxes_FailsWhenAllEndpointsMissing(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		AzdClient: &azdext.AzdClient{},
		assembleState: fixedAssembler(stateWithToolboxes(
			nextstep.ResourceRef{Name: "web-search-tools", ServiceName: "svc-a"},
		)),
		lookupToolboxEnv: fixedToolboxLookup(map[string]string{}),
	}
	res := runToolboxesCheck(t, deps, nil)
	require.Equal(t, StatusFail, res.Status)
	require.Contains(t, res.Message, "1 toolbox(es)")
	require.Equal(t, 0, res.Details["matchedCount"])
}

// Empty / whitespace-only values are treated as unset (matches
// detectMissingVars semantics in nextstep/state.go).
func TestCheckToolboxes_TreatsWhitespaceValueAsMissing(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		AzdClient: &azdext.AzdClient{},
		assembleState: fixedAssembler(stateWithToolboxes(
			nextstep.ResourceRef{Name: "wst", ServiceName: "svc-a"},
		)),
		lookupToolboxEnv: fixedToolboxLookup(map[string]string{
			"TOOLBOX_WST_MCP_ENDPOINT": "   ",
		}),
	}
	res := runToolboxesCheck(t, deps, nil)
	require.Equal(t, StatusFail, res.Status)
}

// ---- Transport error: divergent from C13 (Fail, not Skip) ----

func TestCheckToolboxes_FailsOnEnvLookupTransportError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("grpc: connection refused")
	deps := Dependencies{
		AzdClient: &azdext.AzdClient{},
		assembleState: fixedAssembler(stateWithToolboxes(
			nextstep.ResourceRef{Name: "wst", ServiceName: "svc-a"},
		)),
		lookupToolboxEnv: func(_ context.Context, _ string) (string, error) {
			return "", wantErr
		},
	}
	res := runToolboxesCheck(t, deps, nil)
	require.Equal(t, StatusFail, res.Status, "transport errors must Fail (not Skip) so the user has an actionable signal")
	require.Contains(t, res.Message, "connection refused")
	require.Contains(t, res.Suggestion, "azd env")
}

// ---- Dedup on canonical env key ----

func TestCheckToolboxes_dedupsSameToolboxAcrossServices(t *testing.T) {
	t.Parallel()
	var calls int
	deps := Dependencies{
		AzdClient: &azdext.AzdClient{},
		assembleState: fixedAssembler(stateWithToolboxes(
			nextstep.ResourceRef{Name: "wst", ServiceName: "svc-a"},
			nextstep.ResourceRef{Name: "wst", ServiceName: "svc-b"},
		)),
		lookupToolboxEnv: func(_ context.Context, _ string) (string, error) {
			calls++
			return "https://wst.example/mcp", nil
		},
	}
	res := runToolboxesCheck(t, deps, nil)
	require.Equal(t, StatusPass, res.Status)
	require.Equal(t, 1, calls, "the same canonical env key must be probed at most once")
	require.Equal(t, 1, res.Details["matchedCount"])
}

// ---- normalizeToolboxName / toolboxEndpointKey table ----

func TestNormalizeToolboxName_Table(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"web-search-tools", "WEB_SEARCH_TOOLS"},
		{"WebSearchTools", "WEBSEARCHTOOLS"},
		{"my.toolbox.v2", "MY_TOOLBOX_V2"},
		{"my toolbox", "MY_TOOLBOX"},
		{"alreadyUPPER_NAME", "ALREADYUPPER_NAME"},
		{"mixed-Case.NAME-1", "MIXED_CASE_NAME_1"},
		{"", ""},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, normalizeToolboxName(tc.in), "input=%q", tc.in)
	}
}

func TestToolboxEndpointKey_WrapsNormalizedName(t *testing.T) {
	t.Parallel()
	require.Equal(
		t,
		"TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT",
		toolboxEndpointKey("web-search-tools"),
	)
}

// ---- dedupToolboxKeys helper ----

func TestDedupToolboxKeys(t *testing.T) {
	t.Parallel()
	refs := []nextstep.ResourceRef{
		{Name: "wst", ServiceName: "svc-a"},
		{Name: "wst", ServiceName: "svc-b"},
		{Name: "code-runner", ServiceName: "svc-a"},
	}
	got := dedupToolboxKeys(refs)
	require.Equal(t, []string{
		"TOOLBOX_CODE_RUNNER_MCP_ENDPOINT",
		"TOOLBOX_WST_MCP_ENDPOINT",
	}, got)
}

// ---- Factory-shape pin ----

func TestNewCheckToolboxes_FactoryShape(t *testing.T) {
	t.Parallel()
	c := newCheckToolboxes(Dependencies{})
	require.Equal(t, "local.toolboxes", c.ID)
	require.False(t, c.Remote)
	require.NotNil(t, c.Fn)
	require.NotEmpty(t, c.Name)
}
