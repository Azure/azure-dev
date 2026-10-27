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

	"azureaiagent/internal/featureflags"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

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
