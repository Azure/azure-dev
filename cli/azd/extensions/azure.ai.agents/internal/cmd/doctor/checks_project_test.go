// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"errors"
	"os"
	"path/filepath"
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

func TestCheckProjectEndpointSet_GRPCError_Fails(t *testing.T) {
	t.Parallel()

	client := newTestAzdClient(t,
		&fakeProjectServer{},
		&fakeEnvironmentServer{valueErr: errors.New("rpc boom")})
	check := newCheckProjectEndpointSet(Dependencies{AzdClient: client})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "failed to read AZURE_AI_PROJECT_ENDPOINT")
	require.Contains(t, got.Suggestion, "azd env set AZURE_AI_PROJECT_ENDPOINT")
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

func TestCheckAgentYAMLValid_NoClient_Skips(t *testing.T) {
	t.Parallel()

	check := newCheckAgentYAMLValid(Dependencies{AzdClient: nil})
	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusSkip, got.Status)
}

func TestCheckAgentYAMLValid_PriorAgentDetectionFailed_Skips(t *testing.T) {
	t.Parallel()

	client := newTestAzdClient(t, &fakeProjectServer{}, &fakeEnvironmentServer{})
	check := newCheckAgentYAMLValid(Dependencies{AzdClient: client})

	prior := []Result{{ID: "local.agent-service-detected", Status: StatusFail}}
	got := check.Fn(t.Context(), Options{}, prior)

	require.Equal(t, StatusSkip, got.Status)
	require.Contains(t, got.Message, "no agent services detected")
}

func TestCheckAgentYAMLValid_GRPCError_Fails(t *testing.T) {
	t.Parallel()

	client := newTestAzdClient(t,
		&fakeProjectServer{err: errors.New("rpc boom")},
		&fakeEnvironmentServer{})
	check := newCheckAgentYAMLValid(Dependencies{AzdClient: client})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "failed to get project config")
}

func TestCheckAgentYAMLValid_TransportError_SwapsSuggestion(t *testing.T) {
	t.Parallel()

	client := newTestAzdClient(t,
		&fakeProjectServer{err: status.Error(codes.Unavailable, "transport boom")},
		&fakeEnvironmentServer{})
	check := newCheckAgentYAMLValid(Dependencies{AzdClient: client})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Suggestion, "gRPC channel")
}

func TestCheckAgentYAMLValid_OneServiceValid_Passes(t *testing.T) {
	t.Parallel()

	projectPath := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectPath, "src", "agent"), 0o750))
	writeYAML(t, projectPath, "src/agent/agent.yaml", `
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
	check := newCheckAgentYAMLValid(Dependencies{AzdClient: client})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusPass, got.Status)
	require.Contains(t, got.Message, "agent.yaml valid for 1 service(s)")
}

func TestCheckAgentYAMLValid_NonAgentServicesIgnored(t *testing.T) {
	t.Parallel()

	projectPath := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectPath, "src", "agent"), 0o750))
	writeYAML(t, projectPath, "src/agent/agent.yaml", "name: echo\nlanguage: python\n")

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
	check := newCheckAgentYAMLValid(Dependencies{AzdClient: client})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusPass, got.Status, "api service has no agent.yaml — must be skipped, not failed")
	paths, ok := got.Details["validatedPaths"].([]string)
	require.True(t, ok)
	require.Len(t, paths, 1)
	require.Contains(t, paths[0], "src"+string(filepath.Separator)+"agent")
}

func TestCheckAgentYAMLValid_MissingFile_Fails(t *testing.T) {
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
	check := newCheckAgentYAMLValid(Dependencies{AzdClient: client})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "echo-agent")
	require.Contains(t, got.Suggestion, "Fix the YAML")
	failures, ok := got.Details["failures"].([]string)
	require.True(t, ok)
	require.Len(t, failures, 1)
}

func TestCheckAgentYAMLValid_MalformedYAML_Fails(t *testing.T) {
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
	check := newCheckAgentYAMLValid(Dependencies{AzdClient: client})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "echo-agent")
	failures, ok := got.Details["failures"].([]string)
	require.True(t, ok)
	require.Len(t, failures, 1)
}

func TestCheckAgentYAMLValid_MixedValidAndInvalid_Fails(t *testing.T) {
	t.Parallel()

	projectPath := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectPath, "src", "ok"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(projectPath, "src", "bad"), 0o750))
	writeYAML(t, projectPath, "src/ok/agent.yaml", "name: ok-agent\nlanguage: python\n")
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
	check := newCheckAgentYAMLValid(Dependencies{AzdClient: client})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "1 service(s)") // 1 failure
	require.Contains(t, got.Message, "bad-agent")
	require.NotContains(t, got.Message, "ok-agent: ") // ok-agent should not be in the failures list

	failures, ok := got.Details["failures"].([]string)
	require.True(t, ok)
	require.Len(t, failures, 1)

	validated, ok := got.Details["validatedPaths"].([]string)
	require.True(t, ok)
	require.Len(t, validated, 1)
}

// ---- helper: priorFailed ----

func TestPriorFailed(t *testing.T) {
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
		{"matching skip", []Result{{ID: "x", Status: StatusSkip}}, "x", false},
		{"matching warn", []Result{{ID: "x", Status: StatusWarn}}, "x", false},
		{"different id fail", []Result{{ID: "y", Status: StatusFail}}, "x", false},
		{"id matches middle entry", []Result{
			{ID: "a", Status: StatusPass},
			{ID: "x", Status: StatusFail},
			{ID: "c", Status: StatusPass},
		}, "x", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, priorFailed(tc.prior, tc.id))
		})
	}
}

// writeYAML is a tiny test helper that writes the given content to
// <root>/<rel> after ensuring the parent directory exists.
func writeYAML(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
}
