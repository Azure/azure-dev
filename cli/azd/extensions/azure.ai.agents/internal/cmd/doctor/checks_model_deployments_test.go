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

// validProjectResourceID is a canonical Foundry project ARM resource
// ID used by every model-deployments check test. It must parse cleanly
// through `parseAccountFromProjectID` into
// (subscription=00000000-0000-0000-0000-000000000000, resourceGroup=
// rg-bugbash, accountName=acct-1) so tests can pin the probe arguments.
const validProjectResourceID = "/subscriptions/00000000-0000-0000-0000-000000000000" +
	"/resourceGroups/rg-bugbash" +
	"/providers/Microsoft.CognitiveServices/accounts/acct-1/projects/proj-1"

// healthyModelPrior returns the canonical "all upstream checks passed"
// prior result slice that lets the model-deployments check reach its
// own classification logic. Same shape as `healthyPriorResults` from
// checks_agent_status_test.go but with the extra entries the model
// check evaluates (azure.yaml, agent-yaml-valid).
func healthyModelPrior() []Result {
	return []Result{
		{ID: "local.azure-yaml", Status: StatusPass},
		{ID: "local.environment-selected", Status: StatusPass},
		{ID: "local.agent-service-detected", Status: StatusPass},
		{ID: "remote.auth", Status: StatusPass},
		{ID: "remote.foundry-endpoint", Status: StatusPass},
	}
}

// fixedAssembler returns an assembleState stub that yields the given
// State on every call. Used to inject HasModels / ModelRefs without
// touching disk or invoking the production walker.
func fixedAssembler(
	state *nextstep.State,
) func(context.Context, *azdext.AzdClient) (*nextstep.State, []error) {
	return func(_ context.Context, _ *azdext.AzdClient) (*nextstep.State, []error) {
		return state, nil
	}
}

// fixedProjectIDReader returns a readProjectResourceIDFn that yields
// the supplied id (or error) on every call. Mirrors the rbac /
// agent-identity-roles test pattern so the model-deployments tests
// don't need a real azd env.
func fixedProjectIDReader(
	id string, err error,
) func(context.Context, *azdext.AzdClient) (string, error) {
	return func(_ context.Context, _ *azdext.AzdClient) (string, error) {
		return id, err
	}
}

// fixedDeploymentProbe returns a modelDeploymentProbeFn that yields
// the supplied (names, err). Captures the args it was called with via
// the pointers for in-test assertion of probe routing.
func fixedDeploymentProbe(
	names []string, err error, captured *[]string,
) modelDeploymentProbeFn {
	return func(_ context.Context, sub, rg, account string) ([]string, error) {
		if captured != nil {
			*captured = []string{sub, rg, account}
		}
		return names, err
	}
}

func runModelDeploymentsCheck(t *testing.T, deps Dependencies, prior []Result) Result {
	t.Helper()
	if deps.AzdClient == nil {
		deps.AzdClient = &azdext.AzdClient{}
	}
	c := newCheckModelDeployments(deps)
	require.NotNil(t, c.Fn)
	require.Equal(t, "remote.model-deployments", c.ID)
	require.True(t, c.Remote, "model-deployments check must be tagged remote")
	return c.Fn(t.Context(), Options{}, prior)
}

// ---- Skip-cascade gates ----

func TestCheckModelDeployments_SkipsWhenAzdClientNil(t *testing.T) {
	t.Parallel()
	c := newCheckModelDeployments(Dependencies{})
	res := c.Fn(t.Context(), Options{}, nil)
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "azd extension not reachable")
}

func TestCheckModelDeployments_SkipsCascadeFromUpstream(t *testing.T) {
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
			// Build a prior slice that has only the one failure;
			// the check's guards short-circuit on the first
			// matching priorBlocked, so we don't need a full
			// healthy slice.
			prior := []Result{{ID: tc.failedID, Status: StatusFail}}
			res := runModelDeploymentsCheck(t, Dependencies{}, prior)
			require.Equal(t, StatusSkip, res.Status)
			require.Contains(t, res.Message, tc.wantHint)
		})
	}
}

func TestCheckModelDeployments_SkipsWhenNoManifestModels(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		assembleState:         fixedAssembler(&nextstep.State{HasModels: false}),
		probeModelDeployments: fixedDeploymentProbe(nil, nil, nil),
	}
	res := runModelDeploymentsCheck(t, deps, healthyModelPrior())
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "no model resources declared")
}

func TestCheckModelDeployments_FailsWhenAssemblerReturnsNilState(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		assembleState: func(_ context.Context, _ *azdext.AzdClient) (*nextstep.State, []error) {
			return nil, []error{errors.New("manifest.walker: i/o timeout")}
		},
		probeModelDeployments: fixedDeploymentProbe(nil, nil, nil),
	}
	res := runModelDeploymentsCheck(t, deps, healthyModelPrior())
	require.Equal(t, StatusFail, res.Status)
	require.Contains(t, res.Message, "failed to assemble agent state")
	require.Contains(t, res.Message, "i/o timeout",
		"assembler errs[0] should surface in the Fail message")
}

func TestCheckModelDeployments_SkipsWhenProjectIDUnset(t *testing.T) {
	t.Parallel()
	state := &nextstep.State{
		HasModels: true,
		ModelRefs: []nextstep.ResourceRef{{Name: "gpt-4o", ServiceName: "chat"}},
	}
	deps := Dependencies{
		assembleState:           fixedAssembler(state),
		readProjectResourceIDFn: fixedProjectIDReader("", errors.New("not set")),
	}
	res := runModelDeploymentsCheck(t, deps, healthyModelPrior())
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "AZURE_AI_PROJECT_ID")
}

func TestCheckModelDeployments_SkipsWhenProjectIDUnparsable(t *testing.T) {
	t.Parallel()
	state := &nextstep.State{
		HasModels: true,
		ModelRefs: []nextstep.ResourceRef{{Name: "gpt-4o", ServiceName: "chat"}},
	}
	deps := Dependencies{
		assembleState:           fixedAssembler(state),
		readProjectResourceIDFn: fixedProjectIDReader("garbage", nil),
	}
	res := runModelDeploymentsCheck(t, deps, healthyModelPrior())
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "could not parse account")
}

func TestCheckModelDeployments_SkipsWhenProbeErrors(t *testing.T) {
	t.Parallel()
	state := &nextstep.State{
		HasModels: true,
		ModelRefs: []nextstep.ResourceRef{{Name: "gpt-4o", ServiceName: "chat"}},
	}
	var captured []string
	deps := Dependencies{
		assembleState:           fixedAssembler(state),
		readProjectResourceIDFn: fixedProjectIDReader(validProjectResourceID, nil),
		probeModelDeployments: fixedDeploymentProbe(
			nil, errors.New("ARM transient"), &captured),
	}
	res := runModelDeploymentsCheck(t, deps, healthyModelPrior())
	require.Equal(t, StatusSkip, res.Status)
	require.Contains(t, res.Message, "ARM transient")
	require.NotEmpty(t, res.Suggestion, "transport-error skip must surface retry guidance")
	require.Equal(t,
		[]string{"00000000-0000-0000-0000-000000000000", "rg-bugbash", "acct-1"},
		captured,
		"probe must receive subscription / resourceGroup / accountName parsed from project ID")
}

// ---- Classification ----

func TestCheckModelDeployments_PassesWhenAllRefsMatch(t *testing.T) {
	t.Parallel()
	state := &nextstep.State{
		HasModels: true,
		ModelRefs: []nextstep.ResourceRef{
			{Name: "gpt-4o", ServiceName: "chat"},
			{Name: "embedding-3-large", ServiceName: "search"},
		},
	}
	deps := Dependencies{
		assembleState:           fixedAssembler(state),
		readProjectResourceIDFn: fixedProjectIDReader(validProjectResourceID, nil),
		probeModelDeployments: fixedDeploymentProbe(
			[]string{"gpt-4o", "embedding-3-large", "unrelated-other-model"},
			nil, nil),
	}
	res := runModelDeploymentsCheck(t, deps, healthyModelPrior())
	require.Equal(t, StatusPass, res.Status)
	require.Contains(t, res.Message, "all 2 referenced model deployment(s) present")
	require.Contains(t, res.Message, "acct-1")
	require.EqualValues(t, 2, res.Details["matchedCount"])
}

func TestCheckModelDeployments_FailsWithMissing(t *testing.T) {
	t.Parallel()
	state := &nextstep.State{
		HasModels: true,
		ModelRefs: []nextstep.ResourceRef{
			{Name: "gpt-4o", ServiceName: "chat"},
			{Name: "embedding-3-large", ServiceName: "search"},
			{Name: "gpt-4o-mini", ServiceName: "chat"},
		},
	}
	deps := Dependencies{
		assembleState:           fixedAssembler(state),
		readProjectResourceIDFn: fixedProjectIDReader(validProjectResourceID, nil),
		probeModelDeployments: fixedDeploymentProbe(
			[]string{"gpt-4o"}, // only the first one exists
			nil, nil),
	}
	res := runModelDeploymentsCheck(t, deps, healthyModelPrior())
	require.Equal(t, StatusFail, res.Status)
	require.Contains(t, res.Message, "2 model deployment(s)")
	require.Contains(t, res.Message, "embedding-3-large (service search)")
	require.Contains(t, res.Message, "gpt-4o-mini (service chat)")
	require.Contains(t, res.Suggestion, "azd provision")
	require.EqualValues(t, 1, res.Details["matchedCount"])
}

func TestCheckModelDeployments_FailsWhenAllMissing(t *testing.T) {
	t.Parallel()
	state := &nextstep.State{
		HasModels: true,
		ModelRefs: []nextstep.ResourceRef{
			{Name: "gpt-4o", ServiceName: "chat"},
		},
	}
	deps := Dependencies{
		assembleState:           fixedAssembler(state),
		readProjectResourceIDFn: fixedProjectIDReader(validProjectResourceID, nil),
		probeModelDeployments:   fixedDeploymentProbe([]string{}, nil, nil),
	}
	res := runModelDeploymentsCheck(t, deps, healthyModelPrior())
	require.Equal(t, StatusFail, res.Status)
	require.Contains(t, res.Message, "1 model deployment(s)")
	require.Contains(t, res.Message, "gpt-4o (service chat)")
}

// ---- Parser ----

func TestParseAccountFromProjectID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		input     string
		wantSub   string
		wantRG    string
		wantAcct  string
		wantError bool
	}{
		{
			name:     "canonical case",
			input:    validProjectResourceID,
			wantSub:  "00000000-0000-0000-0000-000000000000",
			wantRG:   "rg-bugbash",
			wantAcct: "acct-1",
		},
		{
			name: "mixed-case segment markers",
			input: "/SUBSCRIPTIONS/sub-1/RESOURCEGROUPS/rg-1" +
				"/providers/Microsoft.CognitiveServices/ACCOUNTS/acct-2/projects/p-2",
			wantSub:  "sub-1",
			wantRG:   "rg-1",
			wantAcct: "acct-2",
		},
		{
			name:      "missing account segment",
			input:     "/subscriptions/s/resourceGroups/rg/providers/Microsoft.CognitiveServices",
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
			sub, rg, acct, err := parseAccountFromProjectID(tc.input)
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantSub, sub)
			require.Equal(t, tc.wantRG, rg)
			require.Equal(t, tc.wantAcct, acct)
		})
	}
}

// ---- Factory wiring ----

func TestNewCheckModelDeployments_FactoryShape(t *testing.T) {
	t.Parallel()
	c := newCheckModelDeployments(Dependencies{})
	require.Equal(t, "remote.model-deployments", c.ID)
	require.NotEmpty(t, c.Name)
	require.True(t, c.Remote, "model deployments check must be tagged remote so --local-only suppresses it")
	require.NotNil(t, c.Fn)
}
