// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ---- Check `local.agent-service-detected` ----

func TestCheckAgentServiceDetected_NoClient_Skips(t *testing.T) {
	t.Parallel()

	check := newCheckAgentServiceDetected(Dependencies{AzdClient: nil})
	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusSkip, got.Status)
	require.Contains(t, got.Message, "azd extension not reachable")
}

func TestCheckAgentServiceDetected_PriorAzureYAMLFailed_Skips(t *testing.T) {
	t.Parallel()

	client := newTestAzdClient(t, &fakeProjectServer{}, &fakeEnvironmentServer{})
	check := newCheckAgentServiceDetected(Dependencies{AzdClient: client})

	prior := []Result{{ID: "local.azure-yaml", Status: StatusFail}}
	got := check.Fn(t.Context(), Options{}, prior)

	require.Equal(t, StatusSkip, got.Status)
	require.Contains(t, got.Message, "azure.yaml check failed")
}

func TestCheckAgentServiceDetected_GRPCError_Fails(t *testing.T) {
	t.Parallel()

	client := newTestAzdClient(t,
		&fakeProjectServer{err: errors.New("rpc boom")},
		&fakeEnvironmentServer{})
	check := newCheckAgentServiceDetected(Dependencies{AzdClient: client})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "failed to get project config")
	require.Contains(t, got.Suggestion, "azd ai agent init")
}

func TestCheckAgentServiceDetected_TransportError_SwapsSuggestion(t *testing.T) {
	t.Parallel()

	for _, code := range []codes.Code{codes.Unavailable, codes.DeadlineExceeded} {
		client := newTestAzdClient(t,
			&fakeProjectServer{err: status.Error(code, "transport boom")},
			&fakeEnvironmentServer{})
		check := newCheckAgentServiceDetected(Dependencies{AzdClient: client})

		got := check.Fn(t.Context(), Options{}, nil)

		require.Equal(t, StatusFail, got.Status, "code=%s", code)
		require.Contains(t, got.Suggestion, "gRPC channel", "code=%s", code)
		require.NotContains(t, got.Suggestion, "azd ai agent init", "code=%s", code)
	}
}

func TestCheckAgentServiceDetected_NilProject_Fails(t *testing.T) {
	t.Parallel()

	client := newTestAzdClient(t,
		&fakeProjectServer{resp: &azdext.GetProjectResponse{}}, // Project: nil
		&fakeEnvironmentServer{})
	check := newCheckAgentServiceDetected(Dependencies{AzdClient: client})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "failed to get project config")
}

func TestCheckAgentServiceDetected_NoAgentService_Fails(t *testing.T) {
	t.Parallel()

	client := newTestAzdClient(t,
		&fakeProjectServer{resp: &azdext.GetProjectResponse{
			Project: &azdext.ProjectConfig{
				Services: map[string]*azdext.ServiceConfig{
					"api": {Name: "api", Host: "containerapp"},
					"web": {Name: "web", Host: "appservice"},
				},
			},
		}},
		&fakeEnvironmentServer{})
	check := newCheckAgentServiceDetected(Dependencies{AzdClient: client})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "no `microsoft.foundry` service found")
	require.Contains(t, got.Suggestion, "azd ai agent init")
}

func TestCheckAgentServiceDetected_OneAgent_Passes(t *testing.T) {
	t.Parallel()

	client := newTestAzdClient(t,
		&fakeProjectServer{resp: &azdext.GetProjectResponse{
			Project: &azdext.ProjectConfig{
				Services: map[string]*azdext.ServiceConfig{
					"api":        {Name: "api", Host: "containerapp"},
					"echo-agent": {Name: "echo-agent", Host: agentHost},
				},
			},
		}},
		&fakeEnvironmentServer{})
	check := newCheckAgentServiceDetected(Dependencies{AzdClient: client})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusPass, got.Status)
	require.Contains(t, got.Message, "1 agent service(s) in azure.yaml: echo-agent")
	require.Equal(t, 1, got.Details["agentServiceCount"])
	require.Equal(t, []string{"echo-agent"}, got.Details["agentServices"])
}

func TestCheckAgentServiceDetected_MultipleAgents_Passes(t *testing.T) {
	t.Parallel()

	client := newTestAzdClient(t,
		&fakeProjectServer{resp: &azdext.GetProjectResponse{
			Project: &azdext.ProjectConfig{
				Services: map[string]*azdext.ServiceConfig{
					"echo-agent": {Name: "echo-agent", Host: agentHost},
					"summarizer": {Name: "summarizer", Host: agentHost},
				},
			},
		}},
		&fakeEnvironmentServer{})
	check := newCheckAgentServiceDetected(Dependencies{AzdClient: client})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusPass, got.Status)
	require.Contains(t, got.Message, "2 agent service(s)")
	require.Contains(t, got.Message, "echo-agent")
	require.Contains(t, got.Message, "summarizer")
	require.Equal(t, 2, got.Details["agentServiceCount"])
}

// ---- Check `local.project-endpoint-set` ----

func TestCheckProjectEndpointSet_NoClient_Skips(t *testing.T) {
	t.Parallel()

	check := newCheckProjectEndpointSet(Dependencies{AzdClient: nil})
	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusSkip, got.Status)
}

func TestCheckProjectEndpointSet_PriorEnvFailed_Skips(t *testing.T) {
	t.Parallel()

	client := newTestAzdClient(t, &fakeProjectServer{}, &fakeEnvironmentServer{})
	check := newCheckProjectEndpointSet(Dependencies{AzdClient: client})

	prior := []Result{{ID: "local.environment-selected", Status: StatusFail}}
	got := check.Fn(t.Context(), Options{}, prior)

	require.Equal(t, StatusSkip, got.Status)
	require.Contains(t, got.Message, "environment check failed")
}

func TestCheckProjectEndpointSet_PriorEnvSkipped_AlsoSkips(t *testing.T) {
	// Covers the cascade: azure-yaml fails -> environment-selected skips ->
	// project-endpoint-set must also skip. Without this propagation, check 5
	// would run against an unloaded env and surface misleading remediation
	// for the wrong root cause.
	t.Parallel()

	client := newTestAzdClient(t,
		&fakeProjectServer{},
		// If the check incorrectly proceeds past the guard it would call
		// GetValue; set valueErr so we'd see the wrong-path Fail in the
		// assertion instead of a quiet Skip.
		&fakeEnvironmentServer{valueErr: errors.New("should not be called")})
	check := newCheckProjectEndpointSet(Dependencies{AzdClient: client})

	prior := []Result{{ID: "local.environment-selected", Status: StatusSkip}}
	got := check.Fn(t.Context(), Options{}, prior)

	require.Equal(t, StatusSkip, got.Status)
	require.Contains(t, got.Message, "environment check failed or skipped")
}

func TestCheckProjectEndpointSet_GRPCError_Fails(t *testing.T) {
	t.Parallel()

	client := newTestAzdClient(t,
		&fakeProjectServer{},
		&fakeEnvironmentServer{valueErr: errors.New("rpc boom")})
	check := newCheckProjectEndpointSet(Dependencies{AzdClient: client})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "failed to read FOUNDRY_PROJECT_ENDPOINT")
	require.Contains(t, got.Suggestion, "azd env set FOUNDRY_PROJECT_ENDPOINT")
}

func TestCheckProjectEndpointSet_TransportError_SwapsSuggestion(t *testing.T) {
	t.Parallel()

	for _, code := range []codes.Code{codes.Unavailable, codes.DeadlineExceeded} {
		client := newTestAzdClient(t,
			&fakeProjectServer{},
			&fakeEnvironmentServer{valueErr: status.Error(code, "transport boom")})
		check := newCheckProjectEndpointSet(Dependencies{AzdClient: client})

		got := check.Fn(t.Context(), Options{}, nil)

		require.Equal(t, StatusFail, got.Status, "code=%s", code)
		require.Contains(t, got.Suggestion, "gRPC channel", "code=%s", code)
		require.NotContains(t, got.Suggestion, "azd env set", "code=%s", code)
	}
}

func TestCheckProjectEndpointSet_EmptyValue_Fails(t *testing.T) {
	t.Parallel()

	for _, val := range []string{"", "   "} {
		client := newTestAzdClient(t,
			&fakeProjectServer{},
			&fakeEnvironmentServer{valueResp: &azdext.KeyValueResponse{
				Key:   projectEndpointVar,
				Value: val,
			}})
		check := newCheckProjectEndpointSet(Dependencies{AzdClient: client})

		got := check.Fn(t.Context(), Options{}, nil)

		require.Equal(t, StatusFail, got.Status, "value=%q", val)
		require.Contains(t, got.Message, "is not set", "value=%q", val)
		require.Contains(t, got.Suggestion, "azd provision", "value=%q", val)
	}
}

func TestCheckProjectEndpointSet_NilResp_Fails(t *testing.T) {
	t.Parallel()

	client := newTestAzdClient(t,
		&fakeProjectServer{},
		&fakeEnvironmentServer{valueResp: nil})
	check := newCheckProjectEndpointSet(Dependencies{AzdClient: client})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
}

func TestCheckProjectEndpointSet_ValidValue_Passes(t *testing.T) {
	t.Parallel()

	const endpoint = "https://my-project.services.ai.azure.com/api/projects/foo"
	client := newTestAzdClient(t,
		&fakeProjectServer{},
		&fakeEnvironmentServer{valueResp: &azdext.KeyValueResponse{
			Key:   projectEndpointVar,
			Value: endpoint,
		}})
	check := newCheckProjectEndpointSet(Dependencies{AzdClient: client})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusPass, got.Status)
	require.Contains(t, got.Message, endpoint)
	require.Equal(t, endpoint, got.Details["projectEndpoint"])
}

// ---- helper: priorBlocked ----

func TestPriorBlocked(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		prior []Result
		id    string
		want  bool
	}{
		{"empty prior", nil, "x", false},
		{"matching fail", []Result{{ID: "x", Status: StatusFail}}, "x", true},
		{"matching pass", []Result{{ID: "x", Status: StatusPass}}, "x", false},
		// Skip propagates blocking: if upstream skipped (because *its* upstream failed),
		// downstream checks must also skip rather than run on broken assumptions.
		{"matching skip", []Result{{ID: "x", Status: StatusSkip}}, "x", true},
		{"matching warn", []Result{{ID: "x", Status: StatusWarn}}, "x", false},
		{"different id fail", []Result{{ID: "y", Status: StatusFail}}, "x", false},
		{"different id skip", []Result{{ID: "y", Status: StatusSkip}}, "x", false},
		{"id matches middle entry fail", []Result{
			{ID: "a", Status: StatusPass},
			{ID: "x", Status: StatusFail},
			{ID: "c", Status: StatusPass},
		}, "x", true},
		{"id matches middle entry skip", []Result{
			{ID: "a", Status: StatusPass},
			{ID: "x", Status: StatusSkip},
			{ID: "c", Status: StatusPass},
		}, "x", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, priorBlocked(tc.prior, tc.id))
		})
	}
}
