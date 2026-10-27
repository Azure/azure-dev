// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"

	"azureaiagent/internal/featureflags"
	"azureaiagent/internal/pkg/agents/agent_api"
)

// Command and action tests.

type tasksAPIMock struct{ mock.Mock }

func (m *tasksAPIMock) ListTasks(ctx context.Context, options agent_api.TaskListOptions) (json.RawMessage, error) {
	args := m.Called(ctx, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	value, ok := args.Get(0).(json.RawMessage)
	if !ok {
		return nil, errors.New("unexpected Tasks mock result")
	}
	return value, args.Error(1)
}

func (m *tasksAPIMock) GetTask(ctx context.Context, id string) (json.RawMessage, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	value, ok := args.Get(0).(json.RawMessage)
	if !ok {
		return nil, errors.New("unexpected Task mock result")
	}
	return value, args.Error(1)
}

func TestTasksCommandContract(t *testing.T) {
	registered, remaining, err := NewRootCommand().Find([]string{"tasks", "show"})
	require.NoError(t, err)
	require.Empty(t, remaining)
	require.Equal(t, "show", registered.Name())
	require.Equal(t, "tasks", registered.Parent().Name())
	cmd := newTasksCommand(&azdext.ExtensionContext{})
	for _, operation := range []string{"list", "show"} {
		child, remaining, err := cmd.Find([]string{operation})
		require.NoError(t, err)
		require.Empty(t, remaining)
		require.NotNil(t, child.RunE)
		require.NotNil(t, child.Flags().Lookup("project-endpoint"))
		if operation == "list" {
			require.NotNil(t, child.Flags().Lookup("agent-name"))
		} else {
			require.Nil(t, child.Flags().Lookup("agent-name"))
		}
		assertOutputFlagOptions(t, child, "json", []string{"json", "table"})
		require.Nil(t, child.Flags().Lookup("output"), "output is SDK-owned")
		for _, flag := range []string{"protocol", "version", "before", "task-id", "agent-endpoint"} {
			require.Nil(t, child.Flags().Lookup(flag))
		}
		features := requiredPreviewFeatures(child)
		require.Contains(t, features, featureflags.LongRunning)
	}
	for _, operation := range []string{"create", "update", "delete", "select", "cancel", "attachments"} {
		_, remaining, err := cmd.Find([]string{operation})
		require.True(t, err != nil || len(remaining) > 0)
	}
	list, _, err := cmd.Find([]string{"list"})
	require.NoError(t, err)
	for _, flag := range []string{
		"limit", "order", "after", "session-id", "status", "source-type", "lease-owner",
		"has-error", "lease-expired", "tag", "omit-attachment-values",
	} {
		require.NotNil(t, list.Flags().Lookup(flag))
	}
	require.Contains(t, list.Long, "last_id")
	require.Contains(t, list.Long, "same filters")
	require.Contains(t, list.Long, "opaque")
}

func TestTasksValidationBeforeFactory(t *testing.T) {
	for _, tt := range []struct {
		operation string
		args      []string
		output    string
		message   string
	}{
		{"list", []string{"--limit=0"}, "", "--limit"},
		{"list", []string{"--limit=101"}, "", "--limit"},
		{"list", []string{"--order=descending"}, "", "--order"},
		{"list", []string{"--status=failed"}, "", "--status"},
		{"list", []string{"--after="}, "", "--after"},
		{"list", []string{"--session-id="}, "", "--session-id"},
		{"list", []string{"--source-type= "}, "", "--source-type"},
		{"list", []string{"--lease-owner="}, "", "--lease-owner"},
		{"list", []string{"--agent-name="}, "", "--agent-name"},
		{"list", []string{"--project-endpoint="}, "", "--project-endpoint"},
		{"list", []string{"--project-endpoint=http://a.services.ai.azure.com/api/projects/p"}, "", "--project-endpoint"},
		{"list", []string{"--project-endpoint=https://a.services.ai.azure.com/api/projects/p/tasks"},
			"", "--project-endpoint"},
		{"list", []string{"--project-endpoint=https://a.services.ai.azure.com/api/projects/p?token=secret"},
			"", "--project-endpoint"},
		{"list", []string{"--tag=missing"}, "", "--tag"},
		{"list", []string{"--tag=:value"}, "", "--tag"},
		{"list", []string{"--tag=key:"}, "", "--tag"},
		{"list", []string{"extra"}, "", "positional"},
		{"list", nil, "yaml", "--output"},
		{"show", nil, "", "exactly one"},
		{"show", []string{"a", "b"}, "", "exactly one"},
		{"show", []string{" "}, "", "non-empty"},
	} {
		t.Run(tt.operation+"/"+strings.Join(tt.args, " ")+tt.output, func(t *testing.T) {
			called := false
			cmd := newTasksOperationCommand(&azdext.ExtensionContext{OutputFormat: tt.output}, tt.operation,
				func(context.Context, *tasksFlags, bool) (*tasksAction, func(), error) {
					called = true
					return nil, nil, errors.New("unexpected factory")
				})
			require.NoError(t, cmd.ParseFlags(tt.args))
			require.ErrorContains(t, cmd.RunE(cmd, cmd.Flags().Args()), tt.message)
			require.False(t, called)
		})
	}
}

func TestTasksGateBeforeFactoryWithoutAuthentication(t *testing.T) {
	for _, tt := range []struct {
		args    []string
		message string
	}{
		{[]string{"list"}, featureflags.LongRunning.ID},
		{[]string{"show", "exact-id"}, featureflags.LongRunning.ID},
		{[]string{"list", "--limit=0"}, "--limit"},
		{[]string{"show", ""}, "non-empty"},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			t.Setenv(featureflags.LongRunning.EnvVar, "false")
			t.Setenv("AZD_ACCESS_TOKEN", "")
			t.Setenv("AZD_SERVER", "127.0.0.1:1")
			called := false
			group := newTasksCommandWithFactory(&azdext.ExtensionContext{},
				func(context.Context, *tasksFlags, bool) (*tasksAction, func(), error) {
					called = true
					return nil, nil, errors.New("unexpected factory/auth/API resolution")
				})
			root := &cobra.Command{Use: "agent", PersistentPreRunE: enforcePreviewFeatures}
			root.AddCommand(group)
			root.SetArgs(append([]string{"tasks"}, tt.args...))
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			require.ErrorContains(t, root.ExecuteContext(t.Context()), tt.message)
			require.False(t, called)
		})
	}
}

func TestTasksFlagsAndInjectedWriter(t *testing.T) {
	for _, format := range []string{"json", "table"} {
		t.Run(format, func(t *testing.T) {
			api := &tasksAPIMock{}
			t.Cleanup(func() { api.AssertExpectations(t) })
			want := agent_api.TaskListOptions{
				AgentName: "deployed", SessionID: "session", Status: "suspended", SourceType: "future_source",
				LeaseOwner: "owner", After: "opaque/+==", Limit: 7, Order: "asc",
				HasError: new(false), LeaseExpired: new(true), Tags: []string{"kind:a:b,c", "kind:second"},
				OmitAttachmentValues: new(format == "table"),
			}
			api.On("ListTasks", mock.Anything, want).
				Return(json.RawMessage(`{"data":[{"id":"task","status":"future_status"}],"has_more":false}`), nil).Once()
			cleaned := false
			group := newTasksCommandWithFactory(&azdext.ExtensionContext{
				OutputFormat: format, NoPrompt: true, Environment: "stage",
			}, func(_ context.Context, flags *tasksFlags, list bool) (*tasksAction, func(), error) {
				require.True(t, list)
				require.True(t, flags.noPrompt)
				require.Equal(t, "stage", flags.environment)
				require.Equal(t, "service", flags.agentName)
				return &tasksAction{api: api, target: &AgentContext{Name: "deployed"}},
					func() { cleaned = true }, nil
			})
			var writer bytes.Buffer
			group.SetOut(&writer)
			group.SetErr(io.Discard)
			group.SetArgs([]string{
				"list", "--agent-name=service", "--session-id=session", "--status=suspended", "--source-type=future_source",
				"--lease-owner=owner", "--after=opaque/+==", "--limit=7", "--order=asc", "--has-error=false",
				"--lease-expired", "--tag=kind:a:b,c", "--tag=kind:second", "--omit-attachment-values=false",
			})
			require.NoError(t, group.ExecuteContext(t.Context()))
			require.True(t, cleaned)
			require.Contains(t, writer.String(), "task")
		})
	}
}

func TestTasksDefaultsAndNoStateWrites(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", directory)
	t.Chdir(directory)
	file := filepath.Join(directory, "config.json")
	original := []byte(`{"activeTask":"unchanged","session":"unchanged","protocol":"unchanged"}`)
	require.NoError(t, os.WriteFile(file, original, 0600))
	for _, operation := range []string{"list", "show"} {
		api := &tasksAPIMock{}
		if operation == "list" {
			api.On("ListTasks", mock.Anything, agent_api.TaskListOptions{AgentName: "worker", Limit: 20, Order: "desc"}).
				Return(json.RawMessage(`{"data":[]}`), nil).Once()
		} else {
			// IDs are passed unchanged, not trimmed, decoded, or interpreted as paths.
			api.On("GetTask", mock.Anything, " id/%2F+? ").Return(json.RawMessage(`{"id":"exact"}`), nil).Once()
		}
		cmd := newTasksOperationCommand(&azdext.ExtensionContext{}, operation,
			func(_ context.Context, flags *tasksFlags, list bool) (*tasksAction, func(), error) {
				require.Equal(t, operation == "list", list)
				return &tasksAction{api: api, target: &AgentContext{Name: "worker"}}, func() {}, nil
			})
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		if operation == "show" {
			cmd.SetArgs([]string{" id/%2F+? "})
		} else {
			cmd.SetArgs(nil)
		}
		require.NoError(t, cmd.ExecuteContext(t.Context()))
		api.AssertExpectations(t)
	}
	contents, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, original, contents)
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestTasksErrorsAreSanitizedAndActionable(t *testing.T) {
	for _, tt := range []struct {
		status  int
		code    string
		message string
		hint    string
	}{
		{403, "Forbidden", "HTTP 403", "preview"},
		{404, "TaskNotFound", "Task not found", "exact task ID"},
		{404, "task_not_found", "Task not found", "exact task ID"},
		{404, "UnsupportedApiVersion", "route or API version", "preview"},
		{400, "UnsupportedApiVersion", "route or API version", "preview"},
		{400, "InvalidApiVersionParameter", "route or API version", "preview"},
		{400, "ApiVersionNotSupported", "route or API version", "preview"},
		{400, "invalid_api_version", "route or API version", "preview"},
		{404, "NotFound", "HTTP 404", "route availability"},
		{400, "BadRequest", "HTTP 400", "filters"},
		{429, "TooManyRequests", "HTTP 429", "wait"},
		{500, "InternalError", "HTTP 500", "retry later"},
	} {
		t.Run(tt.code, func(t *testing.T) {
			err := tasksOperationError(&azcore.ResponseError{StatusCode: tt.status, ErrorCode: tt.code}, "show")
			service, ok := errors.AsType[*azdext.ServiceError](err)
			require.True(t, ok)
			require.Contains(t, service.Message, tt.message)
			require.Contains(t, service.Suggestion, tt.hint)
			require.Equal(t, tt.status, service.StatusCode)
		})
	}
	err := tasksOperationError(&azcore.ResponseError{StatusCode: 404, ErrorCode: "secret\x1b[31m"}, "show")
	require.NotContains(t, err.Error(), "secret")
	require.NotContains(t, tasksOperationError(errors.New("payload secret"), "show").Error(), "secret")
	require.ErrorIs(t, tasksOperationError(context.Canceled, "show"), context.Canceled)
}

// Target resolution tests.

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

// Output tests.

func TestTasksJSONPreservesServiceResponse(t *testing.T) {
	for _, operation := range []string{"list", "show"} {
		t.Run(operation, func(t *testing.T) {
			api := &tasksAPIMock{}
			raw := json.RawMessage(`{"id":"task","unknown":{"integer":90071992547409931234567890,` +
				`"decimal":1.2345678901234567890123456789,"status":"future_status"},` +
				`"last_id":"opaque&<cursor>","data":[{"attachments":{"key":{"secret":"JSON & <> full detail"}}}]}`)
			if operation == "list" {
				api.On("ListTasks", mock.Anything, mock.Anything).Return(raw, nil).Once()
			} else {
				api.On("GetTask", mock.Anything, "id").Return(raw, nil).Once()
			}
			var writer bytes.Buffer
			action := &tasksAction{api: api, flags: &tasksFlags{}, target: &AgentContext{Name: "worker"}, writer: &writer}
			require.NoError(t, action.run(t.Context(), operation, []string{"id"}))
			var compact bytes.Buffer
			require.NoError(t, json.Compact(&compact, writer.Bytes()))
			require.Equal(t, string(raw), compact.String())
			require.Contains(t, writer.String(), "90071992547409931234567890")
			api.AssertExpectations(t)
		})
	}
}

func TestTasksTableResponsiveAndSafe(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	raw := json.RawMessage(`{"data":[{"id":"task\u001b[31m\nFAKE","status":"future_status",` +
		`"agent_name":"worker","session_id":"session","updated_at":1700000000,` +
		`"payload":"payload-secret","error":{"message":"error-secret"},` +
		`"attachments":[{"value":"attachment-secret"}],"unknown":"unknown-secret"}],` +
		`"has_more":true,"last_id":"cursor-secret"}`)
	for _, width := range []int{120, 80, 35} {
		t.Run(strings.Repeat("w", width), func(t *testing.T) {
			var writer bytes.Buffer
			formatter := &output.PrettyTableFormatter{ConsoleWidthFn: func() int { return width }}
			require.NoError(t, writeTasksTableWithFormatter(&writer, raw, true, formatter))
			text := writer.String()
			require.Contains(t, text, "task")
			require.Contains(t, text, "future_status")
			require.NotContains(t, text, "\x1b")
			require.NotContains(t, text, "\nFAKE")
			for _, secret := range []string{
				"payload-secret", "error-secret", "attachment-secret", "unknown-secret", "cursor-secret",
			} {
				require.NotContains(t, text, secret)
			}
			require.Contains(t, text, "--output json")
			require.Contains(t, text, "last_id")
			require.Contains(t, text, "same filters, --order, and --limit")
			if width == 35 {
				require.Contains(t, text, "STATUS:")
			}
		})
	}
}

func TestTasksShowTableSafeDetail(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	raw := json.RawMessage(`{"id":"task","status":"in_progress","agent_name":"worker","session_id":"session",` +
		`"created_at":1700000000,"updated_at":"2024-01-02T03:04:05Z","completed_at":null,` +
		`"lease":{"owner":"owner\u001b[2J","expires_at":"2024-01-02T03:05:05Z"},` +
		`"started_at":"2024-01-02T03:03:05Z",` +
		`"payload":{"secret":"payload-secret"},"error":{"message":"error-secret"},` +
		`"attachments":[{"value":"attachment-secret"}]}`)
	var writer bytes.Buffer
	require.NoError(t, writeTasksTableWithFormatter(&writer, raw, false,
		&output.PrettyTableFormatter{ConsoleWidthFn: func() int { return 35 }}))
	for _, expected := range []string{
		"task", "in_progress", "worker", "session", "2024-01-02T03:04:05Z",
		"Created:", "2023-11-14T22:13:20Z", "Started: 2024-01-02T03:03:05Z",
		"Lease owner: owner", "Lease expiry: 2024-01-02T03:05:05Z", "Has error: true",
	} {
		require.Contains(t, writer.String(), expected)
	}
	for _, secret := range []string{"payload-secret", "error-secret", "attachment-secret", "\x1b"} {
		require.NotContains(t, writer.String(), secret)
	}
}

func TestTasksTableEmptyMissingAndInvalid(t *testing.T) {
	for _, raw := range []string{`{"data":[]}`, `{"data":null}`, `{}`} {
		var writer bytes.Buffer
		require.NoError(t, writeTasksTable(&writer, json.RawMessage(raw), true))
		require.Equal(t, "No Tasks on this page.\n", writer.String())
	}
	var writer bytes.Buffer
	require.NoError(t, writeTasksTable(&writer, json.RawMessage(`{"data":[],"has_more":true}`), true))
	require.Contains(t, writer.String(), "last_id")
	writer.Reset()
	require.NoError(t, writeTasksTableWithFormatter(&writer, json.RawMessage(`{"data":[{}]}`), true,
		&output.PrettyTableFormatter{ConsoleWidthFn: func() int { return 120 }}))
	require.Contains(t, writer.String(), "-")
	for _, raw := range []string{`{`, `{"data":"secret"}`, `{"data":[{"id":{}}]}`} {
		err := writeTasksTable(io.Discard, json.RawMessage(raw), true)
		require.ErrorContains(t, err, "invalid Tasks table response")
		require.NotContains(t, err.Error(), "secret")
	}
}

func TestTaskDisplayTextBounded(t *testing.T) {
	text := taskDisplayText(strings.Repeat("a", 10000))
	require.LessOrEqual(t, len(text), 267)
	require.True(t, strings.HasSuffix(text, "..."))
	for _, value := range []string{"\x1b[2J\n\r\t", "\u009b31m", "a\u202eb", "a\u2028b"} {
		text := taskDisplayText(value)
		require.NotContains(t, text, "\x1b")
		require.NotContains(t, text, "\n")
		require.NotContains(t, text, "\r")
		require.NotContains(t, text, "\u009b")
		require.NotContains(t, text, "\u202e")
	}
}

type tasksFailingWriter struct{ err error }

func (w tasksFailingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestTasksOutputWriterErrors(t *testing.T) {
	failure := errors.New("writer failed")
	for _, tt := range []struct {
		raw  string
		list bool
	}{
		{`{"data":[]}`, true},
		{`{"data":[{"id":"task"}]}`, true},
		{`{"id":"task","has_error":true}`, false},
	} {
		require.ErrorIs(t, writeTasksTable(tasksFailingWriter{failure}, json.RawMessage(tt.raw), tt.list), failure)
	}
}
