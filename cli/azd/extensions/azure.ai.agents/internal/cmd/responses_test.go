// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
		name      string
		operation string
		status    int
		body      string
		getStatus int
		getBody   string
		wantErr   string
		wantCalls []string
		wantText  string
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
				assert.Equal(t, "request-123", r.Header.Get("x-client-request-id"))
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
				flags:         &invokeFlags{userIdentityFlags: userIdentityFlags{userIdentity: "test-user"}},
				credential:    responseTestCredential{},
				clientHeaders: http.Header{"X-Client-Request-Id": []string{"request-123"}},
			}
			rc := &remoteContext{
				projectEndpoint: server.URL, name: "agent", apiVersion: "v1", azdClient: client, agentKey: "agent-key",
			}
			var output bytes.Buffer
			var err error
			switch tt.operation {
			case "show":
				var result responseSnapshotResult
				result, err = action.getResponseSnapshot(t.Context(), rc, "resp_test")
				if err == nil {
					err = printResponseSnapshot(&output, result, "json")
					assert.JSONEq(t, tt.body, output.String())
				}
			case "follow":
				err = action.followResponse(t.Context(), rc, "resp_test", &output)
			case "cancel":
				err = action.cancelResponse(t.Context(), rc, "resp_test", &output)
			}
			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.wantErr)
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

func TestResolveResponseCommandSelection(t *testing.T) {
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
			_, rc, id, err := resolveResponseCommand(t.Context(), &responseCommandFlags{
				agentEndpoint: endpoint, responseID: tt.explicitID,
			})
			require.NoError(t, err)
			defer rc.azdClient.Close()
			assert.Equal(t, tt.want, id)
			var saved map[string]savedResponse
			config.getJSON(t, responsesConfigPath, &saved)
			assert.Equal(t, "resp_current", saved[key].ResponseID)
		})
	}
}

func TestResponsesCommandSubcommands(t *testing.T) {
	cmd := newResponsesCommand(nil)
	for _, name := range []string{"show", "follow", "cancel"} {
		child, _, err := cmd.Find([]string{name})
		require.NoError(t, err)
		assert.Equal(t, name, child.Name())
		assert.NotNil(t, child.Flags().Lookup("response-id"))
		assert.NotNil(t, child.Flags().Lookup("agent-endpoint"))
	}
}

func TestResponsesEndpointRejectsAgentSelector(t *testing.T) {
	cmd := newResponsesShowCommand(nil)
	cmd.SetArgs([]string{
		"--response-id", "resp_123",
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

func TestUserConfigResponseStateStoreRoundTrip(t *testing.T) {
	server := newInvokeUserConfigServer()
	client := newInvokeTestAzdClient(t, server)
	store := newUserConfigResponseStateStore(client)
	want := savedResponse{ResponseID: "resp_123"}

	require.NoError(t, store.Save(t.Context(), "agent-a", want))
	got, err := store.Get(t.Context(), "agent-a")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, want, *got)

	require.NoError(t, store.Delete(t.Context(), "agent-a"))
	got, err = store.Get(t.Context(), "agent-a")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestUserConfigResponseStateStoreReplacesCurrentID(t *testing.T) {
	server := newInvokeUserConfigServer()
	client := newInvokeTestAzdClient(t, server)
	store := newUserConfigResponseStateStore(client)

	require.NoError(t, store.Save(t.Context(), "agent-a", savedResponse{ResponseID: "resp_1"}))
	require.NoError(t, store.Save(t.Context(), "agent-a", savedResponse{ResponseID: "resp_2"}))
	got, err := store.Get(t.Context(), "agent-a")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "resp_2", got.ResponseID)
}

func TestCleanupAgentStateForKey(t *testing.T) {
	t.Parallel()

	const (
		agentKey = "agent-key"
		otherKey = "other-agent-key"
	)
	server := newInvokeUserConfigServer()
	server.setJSON(t, configPath("sessions"), map[string]string{
		agentKey: "sess_123",
		otherKey: "sess_other",
	})
	server.setJSON(t, configPath("conversations"), map[string]string{
		agentKey: "conv_123",
		otherKey: "conv_other",
	})
	server.setJSON(t, responsesConfigPath, map[string]savedResponse{
		agentKey: {ResponseID: "resp_123"},
		otherKey: {ResponseID: "resp_other"},
	})
	client := newInvokeTestAzdClient(t, server)

	require.True(t, cleanupAgentStateForKey(t.Context(), client, agentKey))

	var sessions map[string]string
	server.getJSON(t, configPath("sessions"), &sessions)
	assert.NotContains(t, sessions, agentKey)
	assert.Equal(t, "sess_other", sessions[otherKey])

	var conversations map[string]string
	server.getJSON(t, configPath("conversations"), &conversations)
	assert.NotContains(t, conversations, agentKey)
	assert.Equal(t, "conv_other", conversations[otherKey])

	var responses map[string]savedResponse
	server.getJSON(t, responsesConfigPath, &responses)
	assert.NotContains(t, responses, agentKey)
	assert.Equal(t, "resp_other", responses[otherKey].ResponseID)
}
