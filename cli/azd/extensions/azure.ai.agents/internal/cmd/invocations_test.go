// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// responseTestCredential avoids subprocesses and live credentials in HTTP tests.
type responseTestCredential struct{}

func (responseTestCredential) GetToken(
	_ context.Context, _ policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func TestResponsesHTTP(t *testing.T) {
	const snapshot = `{"id":"resp_test","status":"completed","output":[]}`
	const completed = "event: response.completed\ndata: " +
		`{"response":{"id":"resp_test","status":"completed","output":[` +
		`{"content":[{"type":"output_text","text":"replayed"}]}]}}` + "\n\n"
	const active = "event: response.created\ndata: " +
		`{"response":{"id":"resp_test","status":"in_progress"}}` + "\n\n"
	tests := []struct {
		name           string
		operation      string
		status         int
		body           string
		getStatus      int
		getBody        string
		wantErr        string
		wantCalls      []string
		wantText       string
		wantSuggestion string
	}{
		{name: "show", operation: "show", status: 200, body: snapshot, wantCalls: []string{"GET"}},
		{
			name: "show wrong identity", operation: "show", status: 200,
			body: `{"id":"resp_other","status":"completed"}`, wantErr: "does not match requested ID",
			wantCalls: []string{"GET"},
		},
		{
			name: "completed replay", operation: "follow", status: 200, body: completed,
			wantCalls: []string{"GET"}, wantText: "replayed",
		},
		{
			name: "follow disconnect does not retry", operation: "follow", status: 200, body: active,
			wantErr: "disconnected", wantCalls: []string{"GET"},
		},
		{
			name: "follow service error does not retry", operation: "follow", status: 503,
			body: `{"error":"unavailable"}`, wantErr: "HTTP 503", wantCalls: []string{"GET"},
		},
		{
			name: "follow cancelled or expired stream", operation: "follow", status: 400,
			body: `{"error":{"code":"invalid_request_error","param":"stream","type":"invalid_request_error",` +
				`"message":"This response cannot be streamed because it was not created with stream=true ` +
				`or the stream TTL has expired."}}`,
			wantErr: "Output is unavailable for this invocation", wantCalls: []string{"GET"},
			wantSuggestion: "invocations show",
		},
		{
			name: "follow foreground stream", operation: "follow", status: 400,
			body: `{"error":{"code":"invalid_request_error","param":"stream",` +
				`"message":"This response cannot be streamed because it was not created with background=true."}}`,
			wantErr: "not started with --long-running", wantCalls: []string{"GET"},
			wantSuggestion: "invocations show",
		},
		{
			name: "unrelated follow rejection", operation: "follow", status: 400,
			body:    `{"error":{"code":"invalid_request_error","param":"stream","message":"other error"}}`,
			wantErr: "following Response failed with HTTP 400", wantCalls: []string{"GET"},
		},
		{
			name: "malformed follow rejection", operation: "follow", status: 400, body: "not JSON",
			wantErr: "following Response failed with HTTP 400", wantCalls: []string{"GET"},
		},
		{
			name: "cancelled stream event", operation: "follow", status: 200,
			body: "event: response.cancelled\ndata: " +
				`{"response":{"id":"resp_test","status":"cancelled","output":[]}}` + "\n\n",
			wantErr: "this invocation was cancelled", wantCalls: []string{"GET"},
		},
		{
			name: "cancel", operation: "cancel", status: 200, body: `{"id":"resp_test","status":"cancelled"}`,
			wantCalls: []string{"POST"}, wantText: "is cancelled",
		},
		{
			name: "cancel already completed", operation: "cancel", status: 400, body: `{"error":"terminal"}`,
			getStatus: 200, getBody: snapshot, wantCalls: []string{"POST", "GET"}, wantText: "already completed",
		},
		{
			name: "cancel rejection still active", operation: "cancel", status: 409, body: `{"error":"conflict"}`,
			getStatus: 200, getBody: `{"id":"resp_test","status":"in_progress"}`,
			wantCalls: []string{"POST", "GET"}, wantErr: "HTTP 409",
		},
		{
			name: "cancel rejection snapshot failure", operation: "cancel", status: 403, body: `{"error":"denied"}`,
			getStatus: 404, getBody: `{"error":"not found"}`, wantCalls: []string{"POST", "GET"}, wantErr: "HTTP 403",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.Method)
				assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
				assert.Equal(t, "test-user", r.Header.Get("x-ms-user-identity"))
				assert.Equal(t, "v1", r.URL.Query().Get("api-version"))
				assert.False(t, r.URL.Query().Has("starting_after"))
				path := "/agents/agent/endpoint/protocols/openai/responses/resp_test"
				if r.Method == http.MethodPost {
					path += "/cancel"
				}
				assert.Equal(t, path, r.URL.Path)
				if tt.operation == "follow" {
					assert.Equal(t, "true", r.URL.Query().Get("stream"))
					assert.Equal(t, "text/event-stream", r.Header.Get("Accept"))
				} else {
					assert.False(t, r.URL.Query().Has("stream"))
				}
				if tt.operation == "cancel" && r.Method == http.MethodGet {
					w.WriteHeader(tt.getStatus)
					_, _ = io.WriteString(w, tt.getBody)
					return
				}
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()
			config := newInvokeUserConfigServer()
			client := newInvokeTestAzdClient(t, config)
			store := newUserConfigResponseStateStore(client)
			require.NoError(t, store.Save(t.Context(), "agent-key", savedResponse{ResponseID: "resp_current"}))
			action := &InvokeAction{
				flags: &invokeFlags{
					protocol: "responses", userIdentityFlags: userIdentityFlags{userIdentity: "test-user"},
				},
				credential: responseTestCredential{},
			}
			rc := &remoteContext{
				projectEndpoint: server.URL, name: "agent", apiVersion: "v1", azdClient: client, agentKey: "agent-key",
			}
			var output bytes.Buffer
			err := action.runInvocationOperation(
				t.Context(), rc, "resp_test", invocationOperation(tt.operation), "json", &output,
			)
			if tt.operation == "show" && err == nil {
				assert.JSONEq(t, tt.body, output.String())
			}
			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.wantErr)
			}
			if tt.wantSuggestion != "" {
				serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
				require.True(t, ok)
				assert.Equal(t, tt.status, serviceErr.StatusCode)
				assert.Contains(t, serviceErr.Suggestion, tt.wantSuggestion)
				assert.Contains(t, serviceErr.Suggestion, `--id "resp_test"`)
				assert.NotContains(t, serviceErr.Suggestion, "resp_current")
				assert.NotContains(t, serviceErr.Message, "cancelled", "unavailable replay does not prove cancellation")
			}
			assert.Equal(t, tt.wantCalls, calls)
			if tt.wantText != "" {
				assert.Contains(t, output.String(), tt.wantText)
			}
			current, err := store.Get(t.Context(), "agent-key")
			require.NoError(t, err)
			require.NotNil(t, current)
			assert.Equal(t, "resp_current", current.ResponseID, "explicit operations must not replace current selection")
		})
	}
}

func TestResolveInvocationCommandSelection(t *testing.T) {
	server := grpc.NewServer()
	config := newInvokeUserConfigServer()
	azdext.RegisterUserConfigServiceServer(server, config)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	t.Setenv("AZD_SERVER", listener.Addr().String())
	const endpoint = "https://example.services.ai.azure.com/api/projects/project/agents/agent/" +
		"endpoint/protocols/openai/responses?api-version=v1"
	key := buildAgentKey("https://example.services.ai.azure.com/api/projects/project", "agent", "", false)
	config.setJSON(t, responsesConfigPath, map[string]savedResponse{key: {ResponseID: "resp_current"}})
	for _, tt := range []struct{ name, explicitID, want string }{
		{name: "implicit", want: "resp_current"},
		{name: "explicit", explicitID: "resp_explicit", want: "resp_explicit"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, rc, id, err := resolveInvocationCommand(t.Context(), &invocationCommandFlags{
				agentEndpoint: endpoint, id: tt.explicitID,
			}, invocationShow)
			require.NoError(t, err)
			defer rc.azdClient.Close()
			assert.Equal(t, tt.want, id)
			var saved map[string]savedResponse
			config.getJSON(t, responsesConfigPath, &saved)
			assert.Equal(t, "resp_current", saved[key].ResponseID)
		})
	}
}

func TestInvocationOperationSupport(t *testing.T) {
	for _, protocol := range []agent_api.AgentProtocol{
		agent_api.AgentProtocolResponses, agent_api.AgentProtocolInvocations, agent_api.AgentProtocolA2A,
		"activity", "invocations_ws", "voice",
	} {
		for _, operation := range []invocationOperation{invocationShow, invocationFollow, invocationCancel} {
			assert.Equal(t, protocol == agent_api.AgentProtocolResponses,
				supportsInvocationOperation(protocol, operation), "%s %s", protocol, operation)
		}
	}
}

func TestInvocationsCommandValidation(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{name: "empty ID", args: []string{"show", "--id="}, want: "--id requires a non-empty value"},
		{name: "empty protocol", args: []string{"show", "--protocol="}, want: "--protocol requires a non-empty value"},
		{name: "unsupported protocol", args: []string{"show", "--protocol", "a2a", "--id", "id"},
			want: "invocations show is not supported with the a2a protocol"},
		{name: "unsupported follow", args: []string{"follow", "--protocol", "invocations", "--id", "id"},
			want: "invocations follow is not supported with the invocations protocol"},
		{name: "no message", args: []string{"show", "message"}, want: "unknown command"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newInvocationsCommand(nil)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestInvocationsRejectCreateOnlyFlags(t *testing.T) {
	for _, operation := range []string{"show", "follow", "cancel"} {
		for _, flag := range []string{"version", "client-header"} {
			t.Run(operation+"/"+flag, func(t *testing.T) {
				cmd := newInvocationsCommand(nil)
				cmd.SetOut(io.Discard)
				cmd.SetErr(io.Discard)
				cmd.SetArgs([]string{operation, "--" + flag, "value"})
				require.ErrorContains(t, cmd.Execute(), "unknown flag: --"+flag)
			})
		}
	}
	// These options still belong to create, not lifecycle operations.
	invoke := newInvokeCommand(nil)
	assert.NotNil(t, invoke.Flags().Lookup("version"))
	assert.NotNil(t, invoke.Flags().Lookup("client-header"))
}

func TestCurrentInvocationExplicitIDNeedsNoState(t *testing.T) {
	id, err := resolveCurrentInvocationID(t.Context(), &remoteContext{}, agent_api.AgentProtocolResponses, "resp_explicit")
	require.NoError(t, err)
	assert.Equal(t, "resp_explicit", id)
	_, err = resolveCurrentInvocationID(t.Context(), &remoteContext{}, agent_api.AgentProtocolResponses, "")
	require.ErrorContains(t, err, "current invocation state is unavailable")
}

func TestInvocationsCommandSubcommands(t *testing.T) {
	cmd := newInvocationsCommand(nil)
	for _, name := range []string{"show", "follow", "cancel"} {
		child, _, err := cmd.Find([]string{name})
		require.NoError(t, err)
		assert.Equal(t, name, child.Name())
		assert.NotNil(t, child.Flags().Lookup("id"))
		assert.NotNil(t, child.Flags().Lookup("agent-endpoint"))
	}
}

func TestInvocationsEndpointRejectsAgentSelector(t *testing.T) {
	cmd := newInvocationsShowCommand(nil)
	cmd.SetArgs([]string{
		"--id", "resp_123",
		"--agent-name", "agent",
		"--agent-endpoint",
		"https://example.services.ai.azure.com/api/projects/project/agents/agent/" +
			"endpoint/protocols/openai/responses?api-version=v1",
	})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be combined")
}

func TestPrintResponseSnapshotJSON(t *testing.T) {
	var output bytes.Buffer
	require.NoError(t, printResponseSnapshot(&output, responseSnapshotResult{
		snapshot: responsesSnapshot{ID: "resp_123", Status: "completed"},
		raw:      []byte(`{"id":"resp_123","status":"completed"}`),
	}, "json"))
	assert.JSONEq(t, `{"id":"resp_123","status":"completed"}`, output.String())
}

func TestPrintResponseSnapshotTable(t *testing.T) {
	var output bytes.Buffer
	require.NoError(t, printResponseSnapshot(&output, responseSnapshotResult{
		snapshot: responsesSnapshot{ID: "resp_123", Status: "completed", AgentSessionID: "sess_123"},
	}, "table"))
	assert.Contains(t, output.String(), "Response ID  resp_123")
	assert.Contains(t, output.String(), "Status       completed")
	assert.Contains(t, output.String(), "Session ID   sess_123")
}

func TestReadResponsesSSEReportsIdentityOnce(t *testing.T) {
	stream := "event: response.created\n" +
		"data: {\"type\":\"response.created\",\"sequence_number\":0," +
		"\"response\":{\"id\":\"resp_123\",\"status\":\"queued\"}}\n\n" +
		"event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\"sequence_number\":1," +
		"\"response\":{\"id\":\"resp_123\",\"status\":\"completed\"}}\n\n"
	var responseIDs []string
	var output bytes.Buffer
	err := readResponsesSSE(t.Context(), bytes.NewBufferString(stream), &output, "agent", responsesSSEOptions{
		requireTerminal: true,
		onResponseID: func(responseID string) error {
			responseIDs = append(responseIDs, responseID)
			return nil
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"resp_123"}, responseIDs)
}
