// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type stateStoreAPIMock struct{ mock.Mock }

func stateStoreMockResult[T any](args mock.Arguments) (*T, error) {
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	value, ok := args.Get(0).(*T)
	if !ok {
		return nil, fmt.Errorf("unexpected mock result type %T", args.Get(0))
	}
	return value, args.Error(1)
}

func (m *stateStoreAPIMock) ListStateStores(
	ctx context.Context, agent string, options agent_api.StateStoreListOptions,
) (*agent_api.StateStorePage[agent_api.StateStore], error) {
	return stateStoreMockResult[agent_api.StateStorePage[agent_api.StateStore]](m.Called(ctx, agent, options))
}

func (m *stateStoreAPIMock) GetStateStore(ctx context.Context, agent, store string) (*agent_api.StateStore, error) {
	return stateStoreMockResult[agent_api.StateStore](m.Called(ctx, agent, store))
}

func (m *stateStoreAPIMock) ListStateStoreItemKeys(
	ctx context.Context, agent, store string, options agent_api.StateStoreListOptions,
) (*agent_api.StateStorePage[agent_api.StateStoreItem], error) {
	return stateStoreMockResult[agent_api.StateStorePage[agent_api.StateStoreItem]](m.Called(ctx, agent, store, options))
}

func (m *stateStoreAPIMock) GetStateStoreItem(
	ctx context.Context, agent, store, key string,
) (*agent_api.StateStoreItem, error) {
	return stateStoreMockResult[agent_api.StateStoreItem](m.Called(ctx, agent, store, key))
}

func (m *stateStoreAPIMock) SetStateStoreItem(
	ctx context.Context, agent, store, key string, request agent_api.SetStateStoreItemRequest, etag string,
) (*agent_api.StateStoreItem, error) {
	return stateStoreMockResult[agent_api.StateStoreItem](m.Called(ctx, agent, store, key, request, etag))
}

func (m *stateStoreAPIMock) DeleteStateStoreItem(
	ctx context.Context, agent, store, key, etag string,
) (*agent_api.DeletedStateStoreItem, error) {
	return stateStoreMockResult[agent_api.DeletedStateStoreItem](m.Called(ctx, agent, store, key, etag))
}

type stateStorePromptMock struct {
	azdext.PromptServiceClient
	mock.Mock
}

func (m *stateStorePromptMock) Select(
	ctx context.Context, req *azdext.SelectRequest, _ ...grpc.CallOption,
) (*azdext.SelectResponse, error) {
	return stateStoreMockResult[azdext.SelectResponse](m.Called(ctx, req))
}

func (m *stateStorePromptMock) Confirm(
	ctx context.Context, req *azdext.ConfirmRequest, _ ...grpc.CallOption,
) (*azdext.ConfirmResponse, error) {
	return stateStoreMockResult[azdext.ConfirmResponse](m.Called(ctx, req))
}

func newStateStoreTestAction(t *testing.T) (*stateStoreAction, *stateStoreAPIMock, *invokeUserConfigServer, *bytes.Buffer) {
	t.Helper()
	server := newInvokeUserConfigServer()
	host := newInvokeTestAzdClient(t, server)
	target, err := stateStoreTargetFromEndpoint(
		"https://account.services.ai.azure.com/api/projects/project/agents/worker/endpoint/protocols/invocations")
	require.NoError(t, err)
	api := &stateStoreAPIMock{}
	t.Cleanup(func() { api.AssertExpectations(t) })
	writer := &bytes.Buffer{}
	return &stateStoreAction{
		api: api, host: host, target: target, writer: writer,
		flags: &stateStoreFlags{page: agent_api.StateStoreListOptions{Limit: 20, Order: "desc"}, output: "json"},
	}, api, server, writer
}

func TestStateStoreCommandContract(t *testing.T) {
	cmd := newStateStoresCommand(&azdext.ExtensionContext{})
	for _, path := range []string{"list", "select", "show", "items list", "items show", "items set", "items delete"} {
		t.Run(path, func(t *testing.T) {
			child, remaining, err := cmd.Find(strings.Fields(path))
			require.NoError(t, err)
			require.Empty(t, remaining)
			require.NotNil(t, child.RunE)
			assertOutputFlagOptions(t, child, "json", []string{"json", "table"})
			require.Nil(t, child.Flags().Lookup("output"), "output is SDK-owned")
			for _, flag := range []string{"protocol", "version", "session-id", "conversation-id", "create-only", "before"} {
				require.Nil(t, child.Flags().Lookup(flag))
			}
			require.NotEmpty(t, child.Example)
		})
	}
	for _, path := range []string{"create", "update", "delete", "items create"} {
		_, remaining, err := cmd.Find(strings.Fields(path))
		require.True(t, err != nil || len(remaining) > 0, "out-of-scope command must not resolve: %s", path)
	}
}

func TestStateStoreForwardPaginationContract(t *testing.T) {
	for _, operation := range []string{"list", "items list"} {
		t.Run(operation, func(t *testing.T) {
			cmd := newStateStoreOperationCommand(&azdext.ExtensionContext{}, operation)
			require.ErrorContains(t, cmd.ParseFlags([]string{"--before", "cursor"}), "unknown flag: --before")
			require.Contains(t, cmd.Long, "has_more")
			require.Contains(t, cmd.Long, "last_id")
			require.Contains(t, cmd.Flags().Lookup("after").Usage, "last_id")
			require.Contains(t, cmd.Example, operation+" --limit 2 --order asc")
			require.Contains(t, cmd.Example, `--after "<last_id>"`)
			require.NotContains(t, cmd.Long+cmd.Example, "--before")
		})
	}
}

func TestStateStoreCommandExecution(t *testing.T) {
	a, api, _, _ := newStateStoreTestAction(t)
	value := `{"large":9007199254740993}`
	request := agent_api.SetStateStoreItemRequest{
		Value: json.RawMessage(value), Tags: map[string]string{"kind": "checkpoint"},
	}
	api.On("SetStateStoreItem", mock.Anything, "worker", "explicit", "key", request, `"etag"`).
		Return(&agent_api.StateStoreItem{Key: "key", ETag: `"next"`}, nil).Once()
	cleaned := false
	cmd := newStateStoreCommandWithFactory(
		&azdext.ExtensionContext{OutputFormat: "json", Environment: "alternate"}, "items set",
		func(_ context.Context, flags *stateStoreFlags) (*stateStoreAction, func(), error) {
			require.Equal(t, "alternate", flags.environment)
			a.flags, a.host = flags, nil
			return a, func() { cleaned = true }, nil
		})
	var out, diagnostics bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&diagnostics)
	cmd.SetIn(strings.NewReader(value))
	cmd.SetArgs([]string{
		"key", "--store", "explicit", "--value-file", "-", "--tag", "kind=checkpoint", "--if-match", `"etag"`,
	})
	require.NoError(t, cmd.ExecuteContext(t.Context()))
	require.True(t, cleaned)
	require.True(t, json.Valid(out.Bytes()))
	require.Contains(t, out.String(), `"key": "key"`)
	require.Empty(t, diagnostics.String())
}

func TestStateStoreCommandValidation(t *testing.T) {
	for _, tt := range []struct {
		operation string
		args      []string
		noPrompt  bool
		message   string
	}{
		{"list", []string{"--limit", "0"}, false, "--limit"},
		{"list", []string{"--limit", "101"}, false, "--limit"},
		{"list", []string{"--order", "wrong"}, false, "--order"},
		{"list", []string{"--after", ""}, false, "non-empty"},
		{"list", []string{"--agent-name", "x", "--agent-endpoint", "x"}, false, "--agent-name"},
		{"list", []string{"--agent-endpoint", ""}, false, "non-empty"},
		{"items show", []string{"key", "--store", ""}, false, "non-empty"},
		{"items show", []string{""}, false, "must not be empty"},
		{"items set", []string{"key"}, false, "exactly one"},
		{"items set", []string{"key", "--value", "{}", "--value-file", "x"}, false, "exactly one"},
		{"items set", []string{"key", "--value", "null"}, false, "JSON object"},
		{"items delete", []string{"key"}, true, "--yes"},
		{"select", nil, true, "store name"},
	} {
		t.Run(tt.operation+"/"+strings.Join(tt.args, " "), func(t *testing.T) {
			cmd := newStateStoreOperationCommand(&azdext.ExtensionContext{NoPrompt: tt.noPrompt}, tt.operation)
			require.NoError(t, cmd.ParseFlags(tt.args))
			// Invalid input is rejected by the real command handler before host/auth/API resolution.
			err := cmd.RunE(cmd, cmd.Flags().Args())
			require.ErrorContains(t, err, tt.message)
		})
	}
}

func TestStateStoreReadValue(t *testing.T) {
	file := filepath.Join(t.TempDir(), "checkpoint with spaces.json")
	value := `{"large":9007199254740993,"nested":[null,true]}`
	require.NoError(t, os.WriteFile(file, []byte(value), 0600))
	for _, tt := range []struct {
		name  string
		flags stateStoreFlags
		input string
		want  string
	}{
		{"inline", stateStoreFlags{value: value}, "", value},
		{"file", stateStoreFlags{valueFile: file}, "", value},
		{"stdin", stateStoreFlags{valueFile: "-"}, value, value},
		{"empty object", stateStoreFlags{value: "{}"}, "", "{}"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			request, err := readStateStoreValue(t.Context(), &tt.flags, strings.NewReader(tt.input))
			require.NoError(t, err)
			require.Equal(t, tt.want, string(request.Value))
			require.Nil(t, request.Tags)
		})
	}
	for _, value := range []string{"", "[]", "null", "true", "1", `"s"`, "{} {}", "{"} {
		_, err := readStateStoreValue(t.Context(), &stateStoreFlags{value: value}, strings.NewReader(""))
		require.ErrorContains(t, err, "JSON object")
	}
	_, err := readStateStoreValue(t.Context(), &stateStoreFlags{valueFile: file + ".missing"}, nil)
	require.ErrorContains(t, err, "could not read")
	for _, tags := range [][]string{{""}, {"missing"}, {"=value"}, {"key=a", "key=b"}} {
		_, err := readStateStoreValue(t.Context(), &stateStoreFlags{value: "{}", tags: tags}, nil)
		require.Error(t, err)
	}
	request, err := readStateStoreValue(t.Context(),
		&stateStoreFlags{value: "{}", tags: []string{"key=a=b,c", "empty="}}, nil)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"key": "a=b,c", "empty": ""}, request.Tags)
}

func requireStateStoreCancelled(t *testing.T, err error) {
	t.Helper()
	local, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected structured cancellation, got %v", err)
	require.Equal(t, exterrors.CodeCancelled, local.Code)
}

func TestStateStoreStdinCancellation(t *testing.T) {
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := readStateStoreValue(ctx, &stateStoreFlags{valueFile: "-"}, reader)
		done <- err
	}()
	// This write completes only after the input reader has started, and leaves it blocked
	// waiting for EOF. Cancel must close the pipe instead of leaking the reader goroutine.
	_, err := writer.Write([]byte("{"))
	require.NoError(t, err)
	cancel()
	select {
	case err := <-done:
		requireStateStoreCancelled(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("input reading did not stop on cancellation")
	}
}

func TestStateStoreItemActions(t *testing.T) {
	for _, operation := range []string{"list", "show", "items list", "items show", "items set", "items delete"} {
		t.Run(operation, func(t *testing.T) {
			a, api, server, writer := newStateStoreTestAction(t)
			server.setJSON(t, configPath(stateStoreConfigField), map[string]string{a.target.agentKey: "saved"})
			a.flags.ifMatch, a.flags.yes = `"etag"`, true
			a.request = agent_api.SetStateStoreItemRequest{Value: json.RawMessage(`{"large":9007199254740993}`)}
			item := &agent_api.StateStoreItem{Key: "key", Value: a.request.Value, ETag: `"etag"`}
			var args []string
			switch operation {
			case "list":
				api.On("ListStateStores", mock.Anything, "worker", a.flags.page).
					Return(&agent_api.StateStorePage[agent_api.StateStore]{
						Data: []agent_api.StateStore{{Name: "saved"}},
					}, nil)
			case "show":
				api.On("GetStateStore", mock.Anything, "worker", "saved").Return(&agent_api.StateStore{Name: "saved"}, nil)
			case "items list":
				api.On("ListStateStoreItemKeys", mock.Anything, "worker", "saved", a.flags.page).
					Return(&agent_api.StateStorePage[agent_api.StateStoreItem]{
						Data: []agent_api.StateStoreItem{{Key: "key"}},
					}, nil)
			case "items show":
				args = []string{"key"}
				api.On("GetStateStoreItem", mock.Anything, "worker", "saved", "key").Return(item, nil)
			case "items set":
				args = []string{"key"}
				api.On("SetStateStoreItem", mock.Anything, "worker", "saved", "key", a.request, `"etag"`).Return(item, nil)
			case "items delete":
				args = []string{"key"}
				api.On("DeleteStateStoreItem", mock.Anything, "worker", "saved", "key", `"etag"`).
					Return(&agent_api.DeletedStateStoreItem{Key: "key", Deleted: true}, nil)
			}
			require.NoError(t, a.run(t.Context(), operation, args))
			require.True(t, json.Valid(writer.Bytes()), "stdout must contain only JSON")
			if operation == "items show" || operation == "items set" {
				require.Contains(t, writer.String(), "9007199254740993")
			}
			var selection map[string]string
			server.getJSON(t, configPath(stateStoreConfigField), &selection)
			require.Equal(t, map[string]string{a.target.agentKey: "saved"}, selection)
		})
	}
}

func TestStateStoreExplicitOverrideDoesNotReadOrWriteSelection(t *testing.T) {
	for _, operation := range []string{"show", "items show"} {
		t.Run(operation, func(t *testing.T) {
			a, api, _, _ := newStateStoreTestAction(t)
			a.host = nil // Explicit targeting must not even attempt a UserConfig lookup.
			args := []string{"other"}
			if operation == "show" {
				api.On("GetStateStore", mock.Anything, "worker", "other").Return(&agent_api.StateStore{Name: "other"}, nil)
			} else {
				a.flags.store = "other"
				args = []string{"key"}
				api.On("GetStateStoreItem", mock.Anything, "worker", "other", "key").Return(&agent_api.StateStoreItem{}, nil)
			}
			require.NoError(t, a.run(t.Context(), operation, args))
		})
	}
}

func TestStateStoreDeleteConfirmation(t *testing.T) {
	for _, tt := range []struct {
		name                   string
		yes, noPrompt, confirm bool
		wantErr                bool
	}{
		{"approved", false, false, true, false},
		{"declined", false, false, false, true},
		{"no prompt needs yes", false, true, false, true},
		{"yes", true, true, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, api, _, _ := newStateStoreTestAction(t)
			a.flags.store, a.flags.yes, a.flags.noPrompt = "store", tt.yes, tt.noPrompt
			prompt := &stateStorePromptMock{}
			a.prompt = prompt
			if !tt.yes && !tt.noPrompt {
				prompt.On("Confirm", mock.Anything, mock.MatchedBy(func(req *azdext.ConfirmRequest) bool {
					return strings.Contains(req.Options.Message, `"key"`) &&
						strings.Contains(req.Options.Message, `"store"`) &&
						strings.Contains(req.Options.Message, `"worker"`) && !*req.Options.DefaultValue
				})).Return(&azdext.ConfirmResponse{Value: new(tt.confirm)}, nil).Once()
			}
			if !tt.wantErr {
				api.On("DeleteStateStoreItem", mock.Anything, "worker", "store", "key", "").
					Return(&agent_api.DeletedStateStoreItem{Key: "key", Deleted: true}, nil).Once()
			}
			err := a.run(t.Context(), "items delete", []string{"key"})
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			prompt.AssertExpectations(t)
		})
	}
}

func TestStateStoreActionErrors(t *testing.T) {
	for _, code := range []int{403, 404, 412, 500} {
		a, api, _, writer := newStateStoreTestAction(t)
		a.flags.store = "store"
		api.On("GetStateStoreItem", mock.Anything, "worker", "store", "key").
			Return(nil, &azcore.ResponseError{StatusCode: code}).Once()
		err := a.run(t.Context(), "items show", []string{"key"})
		serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
		require.True(t, ok)
		require.Equal(t, code, serviceErr.StatusCode)
		require.Empty(t, writer.String())
		if code == 412 {
			require.Contains(t, serviceErr.Suggestion, "reconcile")
		}
	}
}

func TestStateStoreTableOutput(t *testing.T) {
	for _, result := range []any{
		&agent_api.StateStore{Name: "store", ItemTTLSeconds: -1},
		&agent_api.StateStoreItem{Key: "key", Value: json.RawMessage(`{"large":9007199254740993}`),
			Tags: map[string]string{"kind": "checkpoint"}, ETag: `"` + strings.Repeat("e", 100) + `"`},
		&agent_api.DeletedStateStoreItem{Key: "key", Deleted: true},
		&agent_api.StateStorePage[agent_api.StateStore]{},
		&agent_api.StateStorePage[agent_api.StateStoreItem]{
			Data:    []agent_api.StateStoreItem{{Key: "key", UpdatedAt: 100}},
			HasMore: true, FirstID: new("first"), LastID: new("next"),
		},
	} {
		var writer bytes.Buffer
		require.NoError(t, writeStateStoreTable(&writer, result))
		require.NotEmpty(t, writer.String())
		if page, ok := result.(*agent_api.StateStorePage[agent_api.StateStoreItem]); ok && page.HasMore {
			require.Contains(t, writer.String(), `Next page: pass --after "next"`)
			require.NotContains(t, writer.String(), "--before")
			require.NotContains(t, writer.String(), "first")
		}
		if item, ok := result.(*agent_api.StateStoreItem); ok {
			require.Contains(t, writer.String(), "9007199254740993")
			require.Contains(t, writer.String(), `Tags: {"kind":"checkpoint"}`)
			require.Contains(t, writer.String(), "ETag: "+item.ETag)
		}
	}
	// Output failures are surfaced, not swallowed after a successful API request.
	require.Error(t, writeStateStoreTable(failingStateStoreWriter{}, &agent_api.StateStore{Name: "store"}))
}

type failingStateStoreWriter struct{}

func (failingStateStoreWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestStateStoresRegistered(t *testing.T) {
	root := NewRootCommand()
	cmd, _, err := root.Find([]string{"state-stores", "items", "set"})
	require.NoError(t, err)
	require.Equal(t, "set", cmd.Name())
}
