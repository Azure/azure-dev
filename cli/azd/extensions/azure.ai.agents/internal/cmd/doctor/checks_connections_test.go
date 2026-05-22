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
	return func(_ context.Context, account, project string) ([]string, error) {
		if captured != nil {
			*captured = []string{account, project}
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

func TestCheckConnections_SkipsWhenNoManifestConnections(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		assembleState:           fixedAssembler(&nextstep.State{HasConnections: false}),
		probeFoundryConnections: fixedConnectionsProbe(nil, nil, nil),
	}
	res := runConnectionsCheck(t, deps, healthyConnectionsPrior())
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "no connection resources declared")
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

func TestCheckConnections_SkipsWhenProjectIDUnset(t *testing.T) {
	t.Parallel()
	state := &nextstep.State{
		HasConnections: true,
		Connections: []nextstep.ResourceRef{
			{Name: "blob-storage", ServiceName: "chat", Detail: "AzureBlob | account"},
		},
	}
	deps := Dependencies{
		assembleState:           fixedAssembler(state),
		readProjectResourceIDFn: fixedProjectIDReader("", errors.New("not set")),
	}
	res := runConnectionsCheck(t, deps, healthyConnectionsPrior())
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "AZURE_AI_PROJECT_ID")
}

func TestCheckConnections_SkipsWhenProjectIDUnparsable(t *testing.T) {
	t.Parallel()
	state := &nextstep.State{
		HasConnections: true,
		Connections: []nextstep.ResourceRef{
			{Name: "blob-storage", ServiceName: "chat"},
		},
	}
	deps := Dependencies{
		assembleState:           fixedAssembler(state),
		readProjectResourceIDFn: fixedProjectIDReader("garbage", nil),
	}
	res := runConnectionsCheck(t, deps, healthyConnectionsPrior())
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "could not parse account / project")
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
		[]string{"acct-1", "proj-1"},
		captured,
		"probe must receive accountName / projectName parsed from project ID")
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
	require.Contains(t, res.Message, "2 connection(s)")
	require.Contains(t, res.Message, "openai-default [AzureOpenAI | https://openai.test] (service chat)")
	require.Contains(t, res.Message, "search-conn [CognitiveSearch | search.test] (service search)")
	require.NotContains(t, res.Message, "blob-storage")
	require.Contains(t, res.Suggestion, "azd provision")
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
	require.Contains(t, res.Message, "1 connection(s)")
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

// ---- Parser ----

func TestParseAccountProjectFromProjectID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		input       string
		wantAccount string
		wantProject string
		wantError   bool
	}{
		{
			name:        "canonical case",
			input:       validProjectResourceID,
			wantAccount: "acct-1",
			wantProject: "proj-1",
		},
		{
			name: "mixed-case segment markers",
			input: "/SUBSCRIPTIONS/sub-1/RESOURCEGROUPS/rg-1" +
				"/providers/Microsoft.CognitiveServices/ACCOUNTS/acct-2/PROJECTS/p-2",
			wantAccount: "acct-2",
			wantProject: "p-2",
		},
		{
			name:      "missing project segment",
			input:     "/subscriptions/s/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/a",
			wantError: true,
		},
		{
			name:      "missing account segment",
			input:     "/subscriptions/s/resourceGroups/rg/providers/Microsoft.CognitiveServices/projects/p",
			wantError: true,
		},
		{
			name:      "garbage input",
			input:     "not-a-resource-id",
			wantError: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			account, project, err := parseAccountProjectFromProjectID(tc.input)
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantAccount, account)
			require.Equal(t, tc.wantProject, project)
		})
	}
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
