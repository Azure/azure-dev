// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
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
	require.Contains(t, got.Message, "no `azure.ai.agent` service found")
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

// ---- Check `local.agent-yaml-valid` ----

func TestCheckAgentDefinitionValid_NoClient_Skips(t *testing.T) {
	t.Parallel()

	check := newCheckAgentDefinitionValid(Dependencies{AzdClient: nil})
	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusSkip, got.Status)
}

func TestCheckAgentDefinitionValid_PriorAgentDetectionFailed_Skips(t *testing.T) {
	t.Parallel()

	client := newTestAzdClient(t, &fakeProjectServer{}, &fakeEnvironmentServer{})
	check := newCheckAgentDefinitionValid(Dependencies{AzdClient: client})

	prior := []Result{{ID: "local.agent-service-detected", Status: StatusFail}}
	got := check.Fn(t.Context(), Options{}, prior)

	require.Equal(t, StatusSkip, got.Status)
	require.Contains(t, got.Message, "no agent services detected")
}

func TestCheckAgentDefinitionValid_PriorAgentDetectionSkipped_AlsoSkips(t *testing.T) {
	// Covers the cascade: azure-yaml fails -> agent-service-detected skips ->
	// agent-yaml-valid must also skip. Without this propagation, check 6
	// would re-fetch the project (failing identically to check 2) and
	// surface a duplicate failure for the same root cause.
	t.Parallel()

	client := newTestAzdClient(t,
		// Server set up to fail if reached, to ensure the guard short-circuits.
		&fakeProjectServer{err: errors.New("should not be called")},
		&fakeEnvironmentServer{})
	check := newCheckAgentDefinitionValid(Dependencies{AzdClient: client})

	prior := []Result{{ID: "local.agent-service-detected", Status: StatusSkip}}
	got := check.Fn(t.Context(), Options{}, prior)

	require.Equal(t, StatusSkip, got.Status)
	require.Contains(t, got.Message, "no agent services detected or upstream check blocked")
}

func TestCheckAgentDefinitionValid_GRPCError_Fails(t *testing.T) {
	t.Parallel()

	client := newTestAzdClient(t,
		&fakeProjectServer{err: errors.New("rpc boom")},
		&fakeEnvironmentServer{})
	check := newCheckAgentDefinitionValid(Dependencies{AzdClient: client})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "failed to get project config")
}

func TestCheckAgentDefinitionValid_TransportError_SwapsSuggestion(t *testing.T) {
	t.Parallel()

	client := newTestAzdClient(t,
		&fakeProjectServer{err: status.Error(codes.Unavailable, "transport boom")},
		&fakeEnvironmentServer{})
	check := newCheckAgentDefinitionValid(Dependencies{AzdClient: client})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Suggestion, "gRPC channel")
}

func TestCheckAgentDefinitionValid_OneServiceValid_Passes(t *testing.T) {
	t.Parallel()

	projectPath := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectPath, "src", "agent"), 0o750))
	writeYAML(t, projectPath, "src/agent/agent.yaml", `
kind: hosted
name: echo-agent
language: python
entrypoint: main.py
protocols:
  - protocol: invocations
    version: "1"
`)

	client := newTestAzdClient(t,
		&fakeProjectServer{resp: &azdext.GetProjectResponse{
			Project: &azdext.ProjectConfig{
				Path: projectPath,
				Services: map[string]*azdext.ServiceConfig{
					"echo-agent": {Name: "echo-agent", Host: agentHost, RelativePath: "src/agent"},
				},
			},
		}},
		&fakeEnvironmentServer{})
	check := newCheckAgentDefinitionValid(Dependencies{AzdClient: client})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusPass, got.Status)
	require.Contains(t, got.Message, "agent definition valid for 1 service(s)")
	validated, ok := got.Details["validatedServices"].([]string)
	require.True(t, ok)
	require.Equal(t, []string{"echo-agent"}, validated)
}

func TestCheckAgentDefinitionValid_InlineWithoutFile_Passes(
	t *testing.T,
) {
	t.Parallel()

	props, err := structpb.NewStruct(map[string]any{
		"kind": "hosted",
		"name": "echo-agent",
	})
	require.NoError(t, err)

	client := newTestAzdClient(t,
		&fakeProjectServer{resp: &azdext.GetProjectResponse{
			Project: &azdext.ProjectConfig{
				Path: t.TempDir(),
				Services: map[string]*azdext.ServiceConfig{
					"echo-agent": {
						Name:                 "echo-agent",
						Host:                 agentHost,
						RelativePath:         "src/agent",
						AdditionalProperties: props,
					},
				},
			},
		}},
		&fakeEnvironmentServer{})
	check := newCheckAgentDefinitionValid(
		Dependencies{AzdClient: client},
	)

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusPass, got.Status)
	require.Contains(t, got.Message, "agent definition valid")
}

func TestCheckAgentDefinitionValid_InlineInvalidKind_Fails(
	t *testing.T,
) {
	t.Parallel()

	props, err := structpb.NewStruct(map[string]any{
		"kind": "nonsense",
		"name": "echo-agent",
	})
	require.NoError(t, err)

	client := newTestAzdClient(t,
		&fakeProjectServer{resp: &azdext.GetProjectResponse{
			Project: &azdext.ProjectConfig{
				Path: t.TempDir(),
				Services: map[string]*azdext.ServiceConfig{
					"echo-agent": {
						Name:                 "echo-agent",
						Host:                 agentHost,
						RelativePath:         "src/agent",
						AdditionalProperties: props,
					},
				},
			},
		}},
		&fakeEnvironmentServer{})
	check := newCheckAgentDefinitionValid(
		Dependencies{AzdClient: client},
	)

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	failures, ok := got.Details["failures"].([]string)
	require.True(t, ok)
	require.Len(t, failures, 1)
	require.Contains(t, failures[0], "template.kind must be one of")
}

func TestCheckAgentDefinitionValid_InlineWinsOverStaleFile(
	t *testing.T,
) {
	t.Parallel()

	projectPath := t.TempDir()
	writeYAML(
		t,
		projectPath,
		"src/agent/agent.yaml",
		"name: broken\n  bad-indent: oops\n",
	)
	props, err := structpb.NewStruct(map[string]any{
		"kind": "hosted",
		"name": "echo-agent",
	})
	require.NoError(t, err)

	client := newTestAzdClient(t,
		&fakeProjectServer{resp: &azdext.GetProjectResponse{
			Project: &azdext.ProjectConfig{
				Path: projectPath,
				Services: map[string]*azdext.ServiceConfig{
					"echo-agent": {
						Name:                 "echo-agent",
						Host:                 agentHost,
						RelativePath:         "src/agent",
						AdditionalProperties: props,
					},
				},
			},
		}},
		&fakeEnvironmentServer{})
	check := newCheckAgentDefinitionValid(
		Dependencies{AzdClient: client},
	)

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusPass, got.Status)
}

func TestCheckAgentDefinitionValid_NonAgentServicesIgnored(t *testing.T) {
	t.Parallel()

	projectPath := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectPath, "src", "agent"), 0o750))
	writeYAML(t, projectPath, "src/agent/agent.yaml", "kind: hosted\nname: echo\nlanguage: python\n")

	client := newTestAzdClient(t,
		&fakeProjectServer{resp: &azdext.GetProjectResponse{
			Project: &azdext.ProjectConfig{
				Path: projectPath,
				Services: map[string]*azdext.ServiceConfig{
					"api":        {Name: "api", Host: "containerapp", RelativePath: "src/api"},
					"echo-agent": {Name: "echo-agent", Host: agentHost, RelativePath: "src/agent"},
				},
			},
		}},
		&fakeEnvironmentServer{})
	check := newCheckAgentDefinitionValid(Dependencies{AzdClient: client})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusPass, got.Status)
	validated, ok := got.Details["validatedServices"].([]string)
	require.True(t, ok)
	require.Equal(t, []string{"echo-agent"}, validated)
}

func TestCheckAgentDefinitionValid_MissingDefinition_Fails(t *testing.T) {
	t.Parallel()

	projectPath := t.TempDir()
	// Note: no agent.yaml file created.

	client := newTestAzdClient(t,
		&fakeProjectServer{resp: &azdext.GetProjectResponse{
			Project: &azdext.ProjectConfig{
				Path: projectPath,
				Services: map[string]*azdext.ServiceConfig{
					"echo-agent": {Name: "echo-agent", Host: agentHost, RelativePath: "src/agent"},
				},
			},
		}},
		&fakeEnvironmentServer{})
	check := newCheckAgentDefinitionValid(Dependencies{AzdClient: client})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "echo-agent")
	require.Contains(t, got.Suggestion, "azure.yaml")
	failures, ok := got.Details["failures"].([]string)
	require.True(t, ok)
	require.Len(t, failures, 1)
}

func TestCheckAgentDefinitionValid_MalformedLegacyYAML_Fails(t *testing.T) {
	t.Parallel()

	projectPath := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectPath, "src", "agent"), 0o750))
	writeYAML(t, projectPath, "src/agent/agent.yaml", "name: echo\n  bad-indent: oops\n: missing-key\n")

	client := newTestAzdClient(t,
		&fakeProjectServer{resp: &azdext.GetProjectResponse{
			Project: &azdext.ProjectConfig{
				Path: projectPath,
				Services: map[string]*azdext.ServiceConfig{
					"echo-agent": {Name: "echo-agent", Host: agentHost, RelativePath: "src/agent"},
				},
			},
		}},
		&fakeEnvironmentServer{})
	check := newCheckAgentDefinitionValid(Dependencies{AzdClient: client})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "echo-agent")
	failures, ok := got.Details["failures"].([]string)
	require.True(t, ok)
	require.Len(t, failures, 1)
}

func TestCheckAgentDefinitionValid_MixedValidAndInvalid_Fails(t *testing.T) {
	t.Parallel()

	projectPath := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectPath, "src", "ok"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(projectPath, "src", "bad"), 0o750))
	writeYAML(t, projectPath, "src/ok/agent.yaml", "kind: hosted\nname: ok-agent\nlanguage: python\n")
	// bad: malformed yaml (mapping key with no value, broken indent).
	writeYAML(t, projectPath, "src/bad/agent.yaml", "name: bad\n  : nope\n\t- tabs-here\n")

	client := newTestAzdClient(t,
		&fakeProjectServer{resp: &azdext.GetProjectResponse{
			Project: &azdext.ProjectConfig{
				Path: projectPath,
				Services: map[string]*azdext.ServiceConfig{
					"ok-agent":  {Name: "ok-agent", Host: agentHost, RelativePath: "src/ok"},
					"bad-agent": {Name: "bad-agent", Host: agentHost, RelativePath: "src/bad"},
				},
			},
		}},
		&fakeEnvironmentServer{})
	check := newCheckAgentDefinitionValid(Dependencies{AzdClient: client})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "1 service(s)") // 1 failure
	require.Contains(t, got.Message, "bad-agent")
	require.NotContains(t, got.Message, "ok-agent: ") // ok-agent should not be in the failures list

	failures, ok := got.Details["failures"].([]string)
	require.True(t, ok)
	require.Len(t, failures, 1)

	validated, ok := got.Details["validatedServices"].([]string)
	require.True(t, ok)
	require.Equal(t, []string{"ok-agent"}, validated)
}

func TestCheckAgentDefinitionValid_MultipleFailures_Aggregates(
	t *testing.T,
) {
	t.Parallel()

	client := newTestAzdClient(t,
		&fakeProjectServer{resp: &azdext.GetProjectResponse{
			Project: &azdext.ProjectConfig{
				Path: t.TempDir(),
				Services: map[string]*azdext.ServiceConfig{
					"agent-b": {
						Name:         "agent-b",
						Host:         agentHost,
						RelativePath: "src/b",
					},
					"agent-a": {
						Name:         "agent-a",
						Host:         agentHost,
						RelativePath: "src/a",
					},
				},
			},
		}},
		&fakeEnvironmentServer{})
	check := newCheckAgentDefinitionValid(
		Dependencies{AzdClient: client},
	)

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	failures, ok := got.Details["failures"].([]string)
	require.True(t, ok)
	require.Len(t, failures, 2)
	require.Contains(t, strings.Join(failures, "\n"), "agent-a:")
	require.Contains(t, strings.Join(failures, "\n"), "agent-b:")
}

func TestSortAgentServices(t *testing.T) {
	t.Parallel()

	services := []*azdext.ServiceConfig{
		{Name: "agent-b", Host: agentHost},
		{Name: "agent-a", Host: agentHost},
	}

	sortAgentServices(services)

	require.Equal(
		t,
		[]string{"agent-a", "agent-b"},
		[]string{services[0].Name, services[1].Name},
	)
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

func TestCheckAgentDefinitionValid_MissingKind_Fails(t *testing.T) {
	// Without explicit `kind:`, ValidateAgentDefinition rejects the manifest
	// because kind is required. Doctor must catch this pre-flight rather
	// than letting deploy be the first place that surfaces it.
	t.Parallel()

	projectPath := t.TempDir()
	writeYAML(t, projectPath, "src/agent/agent.yaml", "name: echo-agent\nlanguage: python\n")

	client := newTestAzdClient(t,
		&fakeProjectServer{resp: &azdext.GetProjectResponse{
			Project: &azdext.ProjectConfig{
				Path: projectPath,
				Services: map[string]*azdext.ServiceConfig{
					"echo-agent": {Name: "echo-agent", Host: agentHost, RelativePath: "src/agent"},
				},
			},
		}},
		&fakeEnvironmentServer{})
	check := newCheckAgentDefinitionValid(Dependencies{AzdClient: client})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "echo-agent")
	failures, ok := got.Details["failures"].([]string)
	require.True(t, ok)
	require.Len(t, failures, 1)
	require.Contains(t, failures[0], "kind")
}

func TestCheckAgentDefinitionValid_InvalidKind_Fails(t *testing.T) {
	// A `kind` that isn't in ValidAgentKinds() (hosted/workflow) must be
	// rejected. Bare yaml.Unmarshal would silently accept this; the
	// production deploy path rejects it via ValidateAgentDefinition.
	t.Parallel()

	projectPath := t.TempDir()
	writeYAML(t, projectPath, "src/agent/agent.yaml", "kind: nonsense\nname: echo-agent\nlanguage: python\n")

	client := newTestAzdClient(t,
		&fakeProjectServer{resp: &azdext.GetProjectResponse{
			Project: &azdext.ProjectConfig{
				Path: projectPath,
				Services: map[string]*azdext.ServiceConfig{
					"echo-agent": {Name: "echo-agent", Host: agentHost, RelativePath: "src/agent"},
				},
			},
		}},
		&fakeEnvironmentServer{})
	check := newCheckAgentDefinitionValid(Dependencies{AzdClient: client})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	failures, ok := got.Details["failures"].([]string)
	require.True(t, ok)
	require.Len(t, failures, 1)
	require.Contains(t, failures[0], "kind")
}

func TestCheckAgentDefinitionValid_InvalidName_Fails(t *testing.T) {
	// Agent name must match `^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`
	// (DNS-style). An underscore is invalid for deployable agent names.
	// Doctor must surface this before deploy, not after.
	t.Parallel()

	projectPath := t.TempDir()
	writeYAML(t, projectPath, "src/agent/agent.yaml", "kind: hosted\nname: My_Agent\nlanguage: python\n")

	client := newTestAzdClient(t,
		&fakeProjectServer{resp: &azdext.GetProjectResponse{
			Project: &azdext.ProjectConfig{
				Path: projectPath,
				Services: map[string]*azdext.ServiceConfig{
					"my-agent": {Name: "my-agent", Host: agentHost, RelativePath: "src/agent"},
				},
			},
		}},
		&fakeEnvironmentServer{})
	check := newCheckAgentDefinitionValid(Dependencies{AzdClient: client})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	failures, ok := got.Details["failures"].([]string)
	require.True(t, ok)
	require.Len(t, failures, 1)
	require.Contains(t, failures[0], "name")
}

// writeYAML is a tiny test helper that writes the given content to
// <root>/<rel> after ensuring the parent directory exists.
func writeYAML(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
}
