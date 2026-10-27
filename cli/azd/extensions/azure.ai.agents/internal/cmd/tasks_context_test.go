// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

const tasksTestEndpoint = "https://account.services.ai.azure.com/api/projects/project"

func TestTasksProjectEndpoint(t *testing.T) {
	for _, endpoint := range []string{
		tasksTestEndpoint, tasksTestEndpoint + "/",
		"https://ACCOUNT.services.ai.azure.com/api/projects/%70roject",
	} {
		got, err := tasksProjectEndpoint(endpoint)
		require.NoError(t, err)
		require.Equal(t, tasksTestEndpoint, got)
	}
	got, err := tasksProjectEndpoint("https://account.services.ai.azure.com/api/projects/my%20project")
	require.NoError(t, err)
	require.Equal(t, "https://account.services.ai.azure.com/api/projects/my%20project", got)
	for _, endpoint := range []string{
		"", "http://account.services.ai.azure.com/api/projects/project",
		"https://example.com/api/projects/project", tasksTestEndpoint + "/tasks/id",
		tasksTestEndpoint + "?api-version=v1", tasksTestEndpoint + "?", tasksTestEndpoint + "#",
		tasksTestEndpoint + "#fragment", "https://user:password@account.services.ai.azure.com/api/projects/project",
		"https://account.services.ai.azure.com/api/projects/", tasksTestEndpoint + "%2fchild",
		"https://account.services.ai.azure.com/api/projects/%2e%2e",
		"https://account.services.ai.azure.com:443/api/projects/project",
	} {
		_, err := tasksProjectEndpoint(endpoint)
		require.Error(t, err, endpoint)
	}
}

func TestTasksShowIndependentOfProject(t *testing.T) {
	// A nil client proves explicit show performs no host RPCs, even with selectors.
	target, err := resolveTasksTarget(t.Context(), nil, tasksTargetOptions{
		projectEndpoint: tasksTestEndpoint, agentName: "ignored", environment: "missing",
	})
	require.NoError(t, err)
	require.Equal(t, &AgentContext{ProjectEndpoint: tasksTestEndpoint}, target)
	project := &helpersProjectServer{err: errors.New("must not read project")}
	env := &testEnvironmentServiceServer{current: &azdext.Environment{Name: "dev"},
		values: map[string]map[string]string{"dev": {"FOUNDRY_PROJECT_ENDPOINT": tasksTestEndpoint}}}
	host := newHelpersTestAzdClient(t, project, &helpersPromptServer{}, env)
	target, err = resolveTasksTarget(t.Context(), host, tasksTargetOptions{})
	require.NoError(t, err)
	require.Equal(t, tasksTestEndpoint, target.ProjectEndpoint)
	require.Empty(t, target.Name)
	require.Empty(t, env.setKeys)
}

func TestTasksShowDeploymentProjectFallback(t *testing.T) {
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", tasksTestEndpoint+"-process")
	for _, tt := range []struct {
		name   string
		values map[string]string
		want   string
		error  string
	}{
		{"single project output", map[string]string{
			"AGENT_FIRST_PROJECT_ENDPOINT": tasksTestEndpoint,
		}, tasksTestEndpoint, ""},
		{"single agent output", map[string]string{
			"AGENT_FIRST_ENDPOINT": tasksTestEndpoint + "/agents/deployed/versions/1",
		}, tasksTestEndpoint, ""},
		{"same canonical projects", map[string]string{
			"AGENT_FIRST_PROJECT_ENDPOINT":  tasksTestEndpoint,
			"AGENT_SECOND_PROJECT_ENDPOINT": "https://ACCOUNT.services.ai.azure.com/api/projects/%70roject/",
		}, tasksTestEndpoint, ""},
		{"different projects", map[string]string{
			"AGENT_FIRST_PROJECT_ENDPOINT":  tasksTestEndpoint,
			"AGENT_SECOND_PROJECT_ENDPOINT": tasksTestEndpoint + "-other",
		}, "", "multiple deployed Foundry projects"},
		{"foundry takes precedence", map[string]string{
			"FOUNDRY_PROJECT_ENDPOINT":     tasksTestEndpoint,
			"AGENT_FIRST_PROJECT_ENDPOINT": tasksTestEndpoint + "-other",
		}, tasksTestEndpoint, ""},
		{"no selected outputs", map[string]string{}, "", "deploy an agent service"},
		{"nonlocal outputs ignored", map[string]string{
			"AGENT_REMOTE_PROJECT_ENDPOINT": tasksTestEndpoint,
		}, "", "deploy an agent service"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := &testEnvironmentServiceServer{
				current:      &azdext.Environment{Name: "default"},
				environments: map[string]*azdext.Environment{"selected": {Name: "selected"}},
				values: map[string]map[string]string{
					"default":  {"FOUNDRY_PROJECT_ENDPOINT": tasksTestEndpoint + "-default"},
					"selected": tt.values,
				},
			}
			prompt := &helpersPromptServer{}
			host := newHelpersTestAzdClient(t, &helpersProjectServer{project: &azdext.ProjectConfig{
				Path: t.TempDir(), Services: map[string]*azdext.ServiceConfig{
					"first":  {Name: "first", Host: AiAgentHost},
					"second": {Name: "second", Host: AiAgentHost},
					"remote": {Name: "remote", Host: "containerapp"},
				},
			}}, prompt, env)
			target, err := resolveTasksTarget(t.Context(), host, tasksTargetOptions{
				environment: "selected", agentName: "ignored",
			})
			if tt.error != "" {
				require.ErrorContains(t, err, tt.error)
				require.ErrorContains(t, err, "--project-endpoint")
			} else {
				require.NoError(t, err)
				require.Equal(t, &AgentContext{ProjectEndpoint: tt.want}, target)
			}
			require.Zero(t, prompt.selectCalls.Load())
			require.Zero(t, env.getCurrentCalls)
			require.Empty(t, env.setKeys)
		})
	}
}

func TestTasksDeployedNameProjectAmbiguity(t *testing.T) {
	for _, sameProject := range []bool{false, true} {
		t.Run(map[bool]string{false: "different projects", true: "same canonical project"}[sameProject], func(t *testing.T) {
			secondEndpoint := tasksTestEndpoint + "-other"
			if sameProject {
				secondEndpoint = "https://ACCOUNT.services.ai.azure.com/api/projects/%70roject/"
			}
			env := &testEnvironmentServiceServer{current: &azdext.Environment{Name: "dev"},
				values: map[string]map[string]string{"dev": {
					"AGENT_FIRST_NAME": "deployed", "AGENT_FIRST_PROJECT_ENDPOINT": tasksTestEndpoint,
					"AGENT_SECOND_NAME": "deployed", "AGENT_SECOND_PROJECT_ENDPOINT": secondEndpoint,
				}}}
			prompt := &helpersPromptServer{}
			host := newHelpersTestAzdClient(t, &helpersProjectServer{project: &azdext.ProjectConfig{
				Path: t.TempDir(), Services: map[string]*azdext.ServiceConfig{
					"first": {Name: "first", Host: AiAgentHost}, "second": {Name: "second", Host: AiAgentHost},
				},
			}}, prompt, env)
			for range 10 {
				target, err := resolveTasksTarget(t.Context(), host, tasksTargetOptions{list: true, agentName: "deployed"})
				if sameProject {
					require.NoError(t, err)
					require.Equal(t, tasksTestEndpoint, target.ProjectEndpoint)
				} else {
					require.ErrorContains(t, err, "matches multiple deployed Foundry projects")
					require.ErrorContains(t, err, "--project-endpoint")
				}
			}
			target, err := resolveTasksTarget(t.Context(), host, tasksTargetOptions{list: true, agentName: "first"})
			require.NoError(t, err)
			require.Equal(t, tasksTestEndpoint, target.ProjectEndpoint)
			require.Zero(t, prompt.selectCalls.Load())
			require.Empty(t, env.setKeys)
		})
	}
}

func TestTasksListSelection(t *testing.T) {
	for _, tt := range []struct {
		name     string
		count    int
		noPrompt bool
		want     string
		error    string
	}{
		{"zero", 0, false, "", "no deployed agent"},
		{"one", 1, true, "deployed-first", ""},
		{"many picker", 2, false, "deployed-second", ""},
		{"many no prompt", 2, true, "", "--no-prompt"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			services := map[string]*azdext.ServiceConfig{
				"undeployed": {Name: "undeployed", Host: AiAgentHost},
			}
			values := map[string]string{"FOUNDRY_PROJECT_ENDPOINT": tasksTestEndpoint}
			for i, service := range []string{"first", "second"} {
				if i < tt.count {
					services[service] = &azdext.ServiceConfig{Name: service, Host: AiAgentHost}
					values["AGENT_"+toServiceKey(service)+"_NAME"] = "deployed-" + service
				}
			}
			env := &testEnvironmentServiceServer{current: &azdext.Environment{Name: "dev"},
				values: map[string]map[string]string{"dev": values}}
			prompt := &helpersPromptServer{selectIndex: 1}
			host := newHelpersTestAzdClient(t, &helpersProjectServer{project: &azdext.ProjectConfig{
				Path: t.TempDir(), Services: services,
			}}, prompt, env)
			target, err := resolveTasksTarget(t.Context(), host, tasksTargetOptions{list: true, noPrompt: tt.noPrompt})
			if tt.error != "" {
				require.ErrorContains(t, err, tt.error)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, target.Name)
				require.Equal(t, tasksTestEndpoint, target.ProjectEndpoint)
			}
			if tt.count == 2 && !tt.noPrompt {
				require.EqualValues(t, 1, prompt.selectCalls.Load())
			} else {
				require.Zero(t, prompt.selectCalls.Load())
			}
			require.Empty(t, env.setKeys)
		})
	}
}

func TestTasksExplicitEndpointAliasScope(t *testing.T) {
	for _, tt := range []struct {
		name, override, agent, want, message string
	}{
		{"same alias", tasksTestEndpoint, "worker", "deployed", ""},
		{"canonical alias", "https://ACCOUNT.services.ai.azure.com/api/projects/%70roject", "worker", "deployed", ""},
		{"other alias is raw", tasksTestEndpoint + "-other", "worker", "worker", ""},
		{"raw name", tasksTestEndpoint, "remote", "remote", ""},
		{"same auto", tasksTestEndpoint, "", "deployed", ""},
		{"other requires name", tasksTestEndpoint + "-other", "", "", "no deployed agent"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := &testEnvironmentServiceServer{current: &azdext.Environment{Name: "dev"},
				values: map[string]map[string]string{"dev": {
					"AGENT_WORKER_NAME": "deployed", "AGENT_WORKER_PROJECT_ENDPOINT": tasksTestEndpoint,
				}}}
			host := newHelpersTestAzdClient(t, &helpersProjectServer{project: &azdext.ProjectConfig{
				Path: t.TempDir(), Services: map[string]*azdext.ServiceConfig{"worker": {Name: "worker", Host: AiAgentHost}},
			}}, &helpersPromptServer{}, env)
			target, err := resolveTasksTarget(t.Context(), host, tasksTargetOptions{
				projectEndpoint: tt.override, agentName: tt.agent, list: true,
			})
			if tt.message != "" {
				require.ErrorContains(t, err, tt.message)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, target.Name)
			}
		})
	}
}

func TestTasksSelectedEnvironment(t *testing.T) {
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", tasksTestEndpoint+"-process")
	for _, agent := range []string{"worker", "alternate-agent", ""} {
		t.Run(agent, func(t *testing.T) {
			env := &testEnvironmentServiceServer{
				current:      &azdext.Environment{Name: "default"},
				environments: map[string]*azdext.Environment{"alternate": {Name: "alternate"}},
				values: map[string]map[string]string{
					"default": {"AGENT_WORKER_NAME": "wrong", "FOUNDRY_PROJECT_ENDPOINT": tasksTestEndpoint + "-wrong"},
					"alternate": {"AGENT_WORKER_NAME": "alternate-agent",
						"AGENT_WORKER_ENDPOINT": tasksTestEndpoint + "/agents/alternate-agent/versions/1"},
				},
			}
			host := newHelpersTestAzdClient(t, &helpersProjectServer{project: &azdext.ProjectConfig{
				Path: t.TempDir(), Services: map[string]*azdext.ServiceConfig{"worker": {Name: "worker", Host: AiAgentHost}},
			}}, &helpersPromptServer{}, env)
			opts := tasksTargetOptions{environment: "alternate", agentName: agent, list: true}
			target, err := resolveTasksTarget(t.Context(), host, opts)
			require.NoError(t, err)
			require.Equal(t, "alternate-agent", target.Name)
			require.Equal(t, tasksTestEndpoint, target.ProjectEndpoint)
			require.Zero(t, env.getCurrentCalls)
			delete(env.values["alternate"], "AGENT_WORKER_ENDPOINT")
			_, err = resolveTasksTarget(t.Context(), host, opts)
			require.ErrorContains(t, err, "no valid Foundry project endpoint")
			opts.environment = "missing"
			_, err = resolveTasksTarget(t.Context(), host, opts)
			require.ErrorContains(t, err, "missing")
			require.Empty(t, env.setKeys)
		})
	}
}

func TestTasksPickerFailures(t *testing.T) {
	for _, tt := range []struct {
		name    string
		index   int32
		err     error
		message string
	}{
		{"invalid index", -1, nil, "selection index"},
		{"cancelled", 0, context.Canceled, "cancel"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := &testEnvironmentServiceServer{current: &azdext.Environment{Name: "dev"},
				values: map[string]map[string]string{"dev": {
					"FOUNDRY_PROJECT_ENDPOINT": tasksTestEndpoint,
					"AGENT_FIRST_NAME":         "first", "AGENT_SECOND_NAME": "second",
				}}}
			prompt := &helpersPromptServer{selectIndex: tt.index, selectErr: tt.err}
			host := newHelpersTestAzdClient(t, &helpersProjectServer{project: &azdext.ProjectConfig{
				Path: t.TempDir(), Services: map[string]*azdext.ServiceConfig{
					"first":  {Name: "first", Host: AiAgentHost},
					"second": {Name: "second", Host: AiAgentHost},
				},
			}}, prompt, env)
			_, err := resolveTasksTarget(t.Context(), host, tasksTargetOptions{list: true})
			if tt.err != nil {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.ErrorContains(t, err, tt.message)
			}
			require.Empty(t, env.setKeys)
		})
	}
}

func TestTasksMissingDeploymentGuidance(t *testing.T) {
	for _, agent := range []string{"", "worker"} {
		t.Run(agent, func(t *testing.T) {
			env := &testEnvironmentServiceServer{current: &azdext.Environment{Name: "dev"},
				values: map[string]map[string]string{"dev": {"FOUNDRY_PROJECT_ENDPOINT": tasksTestEndpoint}}}
			host := newHelpersTestAzdClient(t, &helpersProjectServer{project: &azdext.ProjectConfig{
				Path: t.TempDir(), Services: map[string]*azdext.ServiceConfig{
					"worker": {Name: "worker", Host: AiAgentHost},
				},
			}}, &helpersPromptServer{}, env)
			_, err := resolveTasksTarget(t.Context(), host, tasksTargetOptions{list: true, agentName: agent})
			local, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			require.Contains(t, local.Suggestion, "deploy")
			require.Contains(t, local.Suggestion, "--agent-name")
			require.Contains(t, local.Suggestion, "--project-endpoint")
		})
	}
}

func TestTasksMissingEnvironmentAndCheckout(t *testing.T) {
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", tasksTestEndpoint)
	env := &testEnvironmentServiceServer{}
	host := newHelpersTestAzdClient(t, &helpersProjectServer{err: errors.New("no checkout")},
		&helpersPromptServer{}, env)
	_, err := resolveTasksTarget(t.Context(), host, tasksTargetOptions{})
	require.ErrorContains(t, err, "selected azd environment")
	target, err := resolveTasksTarget(t.Context(), host, tasksTargetOptions{
		projectEndpoint: tasksTestEndpoint, agentName: "remote", list: true,
	})
	require.NoError(t, err)
	require.Equal(t, "remote", target.Name)
	_, err = resolveTasksTarget(t.Context(), host, tasksTargetOptions{projectEndpoint: tasksTestEndpoint, list: true})
	require.Error(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = resolveTasksTarget(ctx, nil, tasksTargetOptions{projectEndpoint: tasksTestEndpoint})
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, env.setKeys)
}
