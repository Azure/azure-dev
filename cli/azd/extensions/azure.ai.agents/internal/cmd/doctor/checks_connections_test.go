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

// fixedConnectionsProbe returns a foundryConnectionsProbeFn that
// yields the supplied (names, err). Captures the args it was called
// with via the pointer for in-test assertion of probe routing.
func fixedConnectionsProbe(
	names []string, err error, captured *[]string,
) foundryConnectionsProbeFn {
	return func(_ context.Context, endpoint string) ([]string, error) {
		if captured != nil {
			*captured = []string{endpoint}
		}
		return names, err
	}
}

func runConnectionsCheck(t *testing.T, deps Dependencies, prior []Result) Result {
	t.Helper()
	if deps.AzdClient == nil {
		deps.AzdClient = &azdext.AzdClient{}
	}
	c := newCheckConnections(deps)
	require.NotNil(t, c.Fn)
	require.Equal(t, "remote.connections", c.ID)
	require.True(t, c.Remote, "connections check must be tagged remote")
	return c.Fn(t.Context(), Options{}, prior)
}

// healthyConnectionsPrior returns the canonical "all upstream checks
// passed" prior slice that lets the connections check reach its own
// classification logic. Same shape as healthyModelPrior used by C13.
func healthyConnectionsPrior() []Result {
	return []Result{
		{ID: "local.azure-yaml", Status: StatusPass},
		{ID: "local.environment-selected", Status: StatusPass},
		{ID: "local.agent-service-detected", Status: StatusPass},
		{ID: "local.project-endpoint-set", Status: StatusPass, Details: map[string]any{
			"projectEndpoint": "https://acct-1.services.ai.azure.com/api/projects/proj-1",
		}},
		{ID: "remote.auth", Status: StatusPass},
		{ID: "remote.foundry-endpoint", Status: StatusPass},
	}
}

// ---- Skip-cascade gates ----

func TestCheckConnections_SkipsWhenAzdClientNil(t *testing.T) {
	t.Parallel()
	c := newCheckConnections(Dependencies{})
	res := c.Fn(t.Context(), Options{}, nil)
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "azd extension not reachable")
}

func TestCheckConnections_SkipsCascadeFromUpstream(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		failedID string
		wantHint string
	}{
		{"environment selected blocked", "local.environment-selected", "local.environment-selected"},
		{"azure.yaml blocked", "local.azure-yaml", "local.azure-yaml"},
		{"agent service detected blocked", "local.agent-service-detected", "local.agent-service-detected"},
		{"auth blocked", "remote.auth", "remote.auth"},
		{"foundry-endpoint blocked", "remote.foundry-endpoint", "remote.foundry-endpoint"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			prior := []Result{{ID: tc.failedID, Status: StatusFail}}
			res := runConnectionsCheck(t, Dependencies{}, prior)
			require.Equal(t, StatusSkip, res.Status)
			require.Contains(t, res.Message, tc.wantHint)
		})
	}
}

// ---- State emptiness ----

func TestCheckConnections_SkipsWhenNoConnectionServices(t *testing.T) {
	t.Parallel()
	var probeCalls int
	deps := Dependencies{
		assembleState: fixedAssembler(&nextstep.State{HasConnections: false}),
		probeFoundryConnections: func(
			_ context.Context, _ string,
		) ([]string, error) {
			probeCalls++
			return nil, nil
		},
	}
	res := runConnectionsCheck(t, deps, healthyConnectionsPrior())
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "no enabled connection services")
	require.Equal(t, 0, probeCalls)
}

func TestCheckConnections_FailsOnLoadErrorsBeforeSkip(t *testing.T) {
	t.Parallel()
	var probeCalls int
	deps := Dependencies{
		assembleState: fixedAssembler(&nextstep.State{
			HasConnections: false,
			ConnectionLoadErrors: []string{
				`connection service "bad-conn" has condition in its resolved $ref; ` +
					`put condition beside host in azure.yaml`,
			},
		}),
		probeFoundryConnections: func(
			_ context.Context, _ string,
		) ([]string, error) {
			probeCalls++
			return nil, nil
		},
	}
	res := runConnectionsCheck(t, deps, healthyConnectionsPrior())
	require.Equal(t, StatusFail, res.Status)
	require.Equal(t, 0, probeCalls)
	require.Contains(t, res.Message, "failed to load configured connections")
	require.Contains(t, res.Message, `connection service "bad-conn"`)
	require.Contains(t, res.Message, "put condition beside host in azure.yaml")
	require.Contains(t, res.Suggestion, "Fix azure.yaml")
	require.Contains(t, res.Suggestion, "azd ai agent doctor")
	require.NotContains(t, res.Suggestion, "azd deploy")
	require.Equal(t, []string{
		`connection service "bad-conn" has condition in its resolved $ref; ` +
			`put condition beside host in azure.yaml`,
	}, res.Details["loadErrors"])
}

func TestCheckConnections_FailsWhenAssemblerReturnsNilState(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		assembleState: func(_ context.Context, _ *azdext.AzdClient) (*nextstep.State, []error) {
			return nil, []error{errors.New("manifest.walker: parse error")}
		},
		probeFoundryConnections: fixedConnectionsProbe(nil, nil, nil),
	}
	res := runConnectionsCheck(t, deps, healthyConnectionsPrior())
	require.Equal(t, StatusFail, res.Status)
	require.Contains(t, res.Message, "failed to assemble agent state")
	require.Contains(t, res.Message, "parse error",
		"assembler errs[0] should surface in the Fail message")
}

func TestCheckConnections_SkipsWithoutPriorEndpoint(t *testing.T) {
	t.Parallel()
	state := &nextstep.State{
		HasConnections: true,
		Connections: []nextstep.ResourceRef{
			{Name: "blob-storage", ServiceName: "chat", Detail: "AzureBlob | account"},
		},
	}
	deps := Dependencies{
		assembleState: fixedAssembler(state),
		readProjectResourceIDFn: func(context.Context, *azdext.AzdClient) (string, error) {
			t.Fatal("must not fall back to the project ARM ID")
			return "", nil
		},
	}
	prior := healthyConnectionsPrior()
	prior[3].Details = nil
	res := runConnectionsCheck(t, deps, prior)
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "FOUNDRY_PROJECT_ENDPOINT")
}

func TestCheckConnections_FailsInvalidEndpointWithoutProbing(t *testing.T) {
	t.Parallel()
	state := &nextstep.State{
		HasConnections: true,
		Connections: []nextstep.ResourceRef{
			{Name: "blob-storage", ServiceName: "chat"},
		},
	}
	var probeCalls int
	deps := Dependencies{
		assembleState: fixedAssembler(state),
		probeFoundryConnections: func(
			_ context.Context, _ string,
		) ([]string, error) {
			probeCalls++
			return nil, nil
		},
	}
	prior := healthyConnectionsPrior()
	prior[3].Details["projectEndpoint"] =
		"https://user:password@example.com/api/projects/proj?sig=secret"
	res := runConnectionsCheck(t, deps, prior)
	require.Equal(t, StatusFail, res.Status)
	require.Contains(t, res.Message, "invalid FOUNDRY_PROJECT_ENDPOINT")
	require.NotContains(t, res.Message, "password")
	require.NotContains(t, res.Message, "secret")
	require.Equal(t, 0, probeCalls)
}

func TestCheckConnections_SkipsWhenProbeErrors(t *testing.T) {
	t.Parallel()
	state := &nextstep.State{
		HasConnections: true,
		Connections: []nextstep.ResourceRef{
			{Name: "blob-storage", ServiceName: "chat"},
		},
	}
	var captured []string
	deps := Dependencies{
		assembleState:           fixedAssembler(state),
		readProjectResourceIDFn: fixedProjectIDReader(validProjectResourceID, nil),
		probeFoundryConnections: fixedConnectionsProbe(
			nil, errors.New("Foundry transient"), &captured),
	}
	res := runConnectionsCheck(t, deps, healthyConnectionsPrior())
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "Foundry transient")
	require.NotEmpty(t, res.Suggestion, "transport-error skip must surface retry guidance")
	require.Equal(t,
		[]string{"https://acct-1.services.ai.azure.com/api/projects/proj-1"},
		captured,
		"probe must receive the validated endpoint from prior checks")
}

// ---- Classification ----

func TestCheckConnections_PassesWhenAllRefsMatch(t *testing.T) {
	t.Parallel()
	state := &nextstep.State{
		HasConnections: true,
		Connections: []nextstep.ResourceRef{
			{Name: "blob-storage", ServiceName: "chat", Detail: "AzureBlob | acct"},
			{Name: "openai-default", ServiceName: "chat", Detail: "AzureOpenAI | https://openai.test"},
		},
	}
	deps := Dependencies{
		assembleState:           fixedAssembler(state),
		readProjectResourceIDFn: fixedProjectIDReader(validProjectResourceID, nil),
		probeFoundryConnections: fixedConnectionsProbe(
			[]string{"blob-storage", "openai-default", "unrelated-other"},
			nil, nil),
	}
	res := runConnectionsCheck(t, deps, healthyConnectionsPrior())
	require.Equal(t, StatusPass, res.Status)
	require.Contains(t, res.Message, "all 2 referenced connection(s) present")
	require.Contains(t, res.Message, "proj-1")
	require.EqualValues(t, 2, res.Details["matchedCount"])
	require.Equal(t, "proj-1", res.Details["project"])
	require.Equal(t, "acct-1", res.Details["account"])
}

func TestCheckConnections_FailsWithMissing(t *testing.T) {
	t.Parallel()
	state := &nextstep.State{
		HasConnections: true,
		Connections: []nextstep.ResourceRef{
			{Name: "blob-storage", ServiceName: "chat", Detail: "AzureBlob | acct"},
			{Name: "openai-default", ServiceName: "chat", Detail: "AzureOpenAI | https://openai.test"},
			{Name: "search-conn", ServiceName: "search", Detail: "CognitiveSearch | search.test"},
		},
	}
	deps := Dependencies{
		assembleState:           fixedAssembler(state),
		readProjectResourceIDFn: fixedProjectIDReader(validProjectResourceID, nil),
		probeFoundryConnections: fixedConnectionsProbe(
			[]string{"blob-storage"}, // only first exists
			nil, nil),
	}
	res := runConnectionsCheck(t, deps, healthyConnectionsPrior())
	require.Equal(t, StatusFail, res.Status)
	require.Contains(t, res.Message, "2 configured connection(s)")
	require.Contains(t, res.Message, "openai-default [AzureOpenAI | https://openai.test] (service chat)")
	require.Contains(t, res.Message, "search-conn [CognitiveSearch | search.test] (service search)")
	require.NotContains(t, res.Message, "blob-storage")
	require.Contains(t, res.Suggestion, "azd deploy --all")
	require.Contains(t, res.Suggestion, "azure.ai.connection services")
	require.NotContains(t, res.Suggestion, "azd provision")
	require.NotContains(t, res.Suggestion, "legacy")
	require.Contains(t, res.Suggestion, "agent uses before deploying")
	require.EqualValues(t, 1, res.Details["matchedCount"])
}

func TestCheckConnections_FailsWhenAllMissing(t *testing.T) {
	t.Parallel()
	state := &nextstep.State{
		HasConnections: true,
		Connections: []nextstep.ResourceRef{
			{Name: "blob-storage", ServiceName: "chat"},
		},
	}
	deps := Dependencies{
		assembleState:           fixedAssembler(state),
		readProjectResourceIDFn: fixedProjectIDReader(validProjectResourceID, nil),
		probeFoundryConnections: fixedConnectionsProbe([]string{}, nil, nil),
	}
	res := runConnectionsCheck(t, deps, healthyConnectionsPrior())
	require.Equal(t, StatusFail, res.Status)
	require.Contains(t, res.Message, "1 configured connection(s)")
	require.Contains(t, res.Message, "blob-storage (service chat)")
}

// When Detail is empty the missing-list entry omits the bracketed
// suffix instead of emitting an empty `[]`.
func TestCheckConnections_MissingEntryOmitsEmptyDetail(t *testing.T) {
	t.Parallel()
	state := &nextstep.State{
		HasConnections: true,
		Connections: []nextstep.ResourceRef{
			{Name: "anon-conn", ServiceName: "chat"},
		},
	}
	deps := Dependencies{
		assembleState:           fixedAssembler(state),
		readProjectResourceIDFn: fixedProjectIDReader(validProjectResourceID, nil),
		probeFoundryConnections: fixedConnectionsProbe(nil, nil, nil),
	}
	res := runConnectionsCheck(t, deps, healthyConnectionsPrior())
	require.Equal(t, StatusFail, res.Status)
	require.Contains(t, res.Message, "anon-conn (service chat)")
	require.NotContains(t, res.Message, "[]")
}

func TestCheckConnections_CustomSubdomainDoesNotUseARMAccountName(t *testing.T) {
	t.Parallel()
	const endpoint = "https://custom-dns.services.ai.azure.com/api/projects/proj-1"
	var captured []string
	deps := Dependencies{
		assembleState: fixedAssembler(&nextstep.State{
			HasConnections: true,
			Connections:    []nextstep.ResourceRef{{Name: "registry", ServiceName: "agent"}},
		}),
		readProjectResourceIDFn: func(context.Context, *azdext.AzdClient) (string, error) {
			t.Fatal("connection discovery must not read the ARM ID")
			return "", nil
		},
		probeFoundryConnections: fixedConnectionsProbe([]string{"registry"}, nil, &captured),
	}
	prior := healthyConnectionsPrior()
	prior[3].Details["projectEndpoint"] = endpoint
	result := runConnectionsCheck(t, deps, prior)
	require.Equal(t, StatusPass, result.Status)
	require.Equal(t, []string{endpoint}, captured)
	require.Equal(t, "custom-dns", result.Details["account"])
}

// ---- Factory wiring ----

func TestNewCheckConnections_FactoryShape(t *testing.T) {
	t.Parallel()
	c := newCheckConnections(Dependencies{})
	require.Equal(t, "remote.connections", c.ID)
	require.NotEmpty(t, c.Name)
	require.True(t, c.Remote, "connections check must be tagged remote so --local-only suppresses it")
	require.NotNil(t, c.Fn)
}
