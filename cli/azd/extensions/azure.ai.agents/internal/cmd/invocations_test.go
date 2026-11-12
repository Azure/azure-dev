// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/useragent"

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
				assert.Equal(t, useragent.Default(), r.Header.Get("User-Agent"))
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
			store := newResponseStateStore(client)
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
	const endpoint = "https://example.services.ai.azure.com/api/projects/project/agents/agent/endpoint/protocols/"
	key := buildAgentKey("https://example.services.ai.azure.com/api/projects/project", "agent", "", false)
	saveAgentStateForTest(t, nil, key, agentStateRecord{
		Response:   &savedResponse{ResponseID: "resp_current"},
		Invocation: &savedInvocation{InvocationID: "inv_current"},
	})
	for _, tt := range []struct{ name, path, protocol, explicitID, want string }{
		{name: "Responses implicit", path: "openai/responses", protocol: "responses", want: "resp_current"},
		{name: "Responses explicit", path: "openai/responses", protocol: "responses",
			explicitID: "resp_explicit", want: "resp_explicit"},
		{name: "Invocations implicit", path: "invocations", protocol: "invocations", want: "inv_current"},
		{name: "Invocations explicit", path: "invocations", protocol: "invocations",
			explicitID: "inv_explicit", want: "inv_explicit"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			action, rc, id, err := resolveInvocationCommand(t.Context(), &invocationCommandFlags{
				agentEndpoint: endpoint + tt.path + "?api-version=v1", id: tt.explicitID,
			}, invocationShow)
			require.NoError(t, err)
			defer rc.azdClient.Close()
			assert.Equal(t, tt.want, id)
			assert.Equal(t, tt.protocol, action.flags.protocol)
			record := loadAgentStateForTest(t, rc.azdClient, key)
			require.NotNil(t, record)
			require.NotNil(t, record.Invocation)
			require.NotNil(t, record.Response)
			assert.Equal(t, "inv_current", record.Invocation.InvocationID)
			assert.Equal(t, "resp_current", record.Response.ResponseID)
		})
	}
}

func TestInvocationOperationSupport(t *testing.T) {
	for _, protocol := range []agent_api.AgentProtocol{
		agent_api.AgentProtocolResponses, agent_api.AgentProtocolInvocations, agent_api.AgentProtocolA2A,
		"activity", "invocations_ws", "voice",
	} {
		for _, operation := range []invocationOperation{invocationShow, invocationFollow, invocationCancel} {
			want := protocol == agent_api.AgentProtocolResponses ||
				(protocol == agent_api.AgentProtocolInvocations && operation != invocationFollow)
			assert.Equal(t, want, supportsInvocationOperation(protocol, operation), "%s %s", protocol, operation)
		}
	}
}

func TestResolveInvocationCommandProjectSkipsLiveCapabilities(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())
	// Keep a regressed metadata read from invoking the user's azd credentials.
	t.Setenv("PATH", t.TempDir())
	const projectEndpoint = "https://account.services.ai.azure.com/api/projects/project"
	projectServer := &helpersProjectServer{project: &azdext.ProjectConfig{
		Path: t.TempDir(),
		Services: map[string]*azdext.ServiceConfig{
			"target": {
				Name: "target", Host: AiAgentHost, AdditionalProperties: mustStruct(t, map[string]any{
					"kind": "hosted", "name": "agent",
					"protocols": []any{map[string]any{"protocol": "invocations", "version": "2.0.0"}},
				}),
			},
		},
	}}
	envServer := &testEnvironmentServiceServer{
		current: &azdext.Environment{Name: "test"},
		values: map[string]map[string]string{"test": {
			"AGENT_TARGET_NAME":                       "agent",
			"AGENT_TARGET_PROJECT_ENDPOINT":           projectEndpoint,
			"AGENT_TARGET_RESPONSES_ENDPOINT":         projectEndpoint + "/agents/agent/endpoint/protocols/openai/responses",
			"AGENT_TARGET_PROTOCOL_ENDPOINTS_VERSION": "1",
		}},
	}
	t.Setenv("AZD_SERVER", newInvokeRemoteContextTestAzdServer(t, projectServer, envServer))
	key := buildAgentKey(projectEndpoint, "agent", "", false)
	for _, tt := range []struct {
		name, selected, explicit, id, wantProtocol, wantID, wantError string
		responses, invocations, deleteCurrent                         bool
	}{
		{name: "current Responses", responses: true, invocations: true, selected: "responses",
			wantProtocol: "responses", wantID: "resp_current"},
		{name: "current Invocations", responses: true, invocations: true, selected: "invocations",
			wantProtocol: "invocations", wantID: "inv_current"},
		{name: "explicit ID preserves current selection", responses: true, invocations: true, selected: "invocations",
			id: "inv_explicit", wantProtocol: "invocations", wantID: "inv_explicit"},
		{name: "legacy Responses", responses: true, wantProtocol: "responses", wantID: "resp_current"},
		{name: "legacy Invocations", invocations: true, wantProtocol: "invocations", wantID: "inv_current"},
		{name: "ambiguous legacy state", responses: true, invocations: true,
			wantError: "multiple protocols"},
		{name: "explicit protocol preserves selection", responses: true, invocations: true,
			selected: "responses", explicit: "invocations", wantProtocol: "invocations", wantID: "inv_current"},
		{name: "deleted current work does not select older Response", responses: true, invocations: true,
			selected: "invocations", deleteCurrent: true, wantProtocol: "invocations",
			wantError: "no current invocation is saved"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			state := agentStateRecord{}
			if tt.responses {
				state.Response = &savedResponse{ResponseID: "resp_current"}
			}
			if tt.invocations {
				state.Invocation = &savedInvocation{InvocationID: "inv_current"}
			}
			saveAgentStateForTest(t, nil, key, state)
			switch tt.selected {
			case "responses":
				require.NoError(t, newResponseStateStore(nil).Save(t.Context(), key, *state.Response))
			case "invocations":
				store := newInvocationStateStore(nil)
				require.NoError(t, store.Save(t.Context(), key, *state.Invocation))
				if tt.deleteCurrent {
					require.NoError(t, store.Delete(t.Context(), key))
				}
			}
			for _, operation := range []invocationOperation{invocationShow, invocationFollow, invocationCancel} {
				t.Run(string(operation), func(t *testing.T) {
					action, rc, id, err := resolveInvocationCommand(t.Context(), &invocationCommandFlags{
						agentName: "target", protocol: tt.explicit, id: tt.id, noPrompt: true,
					}, operation)
					wantError := tt.wantError
					if operation == invocationFollow && tt.wantProtocol == "invocations" {
						wantError = "invocations follow is not supported with the invocations protocol"
					}
					if wantError != "" {
						require.ErrorContains(t, err, wantError)
						if tt.wantProtocol == "" {
							require.Contains(t, azdext.WrapError(err).GetSuggestion(), "--protocol")
						}
						return
					}
					require.NoError(t, err)
					defer rc.azdClient.Close()
					assert.Equal(t, tt.wantProtocol, action.flags.protocol)
					assert.Equal(t, tt.wantID, id)
					assert.False(t, rc.agentRead, "lifecycle resolution must not attempt a live capability read")
					record := loadAgentStateForTest(t, rc.azdClient, key)
					require.NotNil(t, record)
					if tt.responses {
						require.NotNil(t, record.Response)
						assert.Equal(t, "resp_current", record.Response.ResponseID)
					}
					if tt.invocations {
						require.NotNil(t, record.Invocation)
						assert.Equal(t, "inv_current", record.Invocation.InvocationID)
					}
					if tt.explicit != "" {
						selected, target, savedID, err := resolveInvocationCommand(t.Context(),
							&invocationCommandFlags{agentName: "target", noPrompt: true}, invocationShow)
						require.NoError(t, err)
						defer target.azdClient.Close()
						assert.Equal(t, "responses", selected.flags.protocol)
						assert.Equal(t, "resp_current", savedID)
					}
				})
			}
		})
	}
}

func TestLegacyInvocationKeysRequireExplicitSelection(t *testing.T) {
	for _, protocol := range []string{"responses", "invocations"} {
		t.Run(protocol, func(t *testing.T) {
			endpoint, _ := newVersionedSessionTestProject(t)
			currentKey := buildAgentKey(endpoint, "agent", "", false)
			deployedKey := buildAgentKey(endpoint, "agent", "2", false)
			saveAgentStateForTest(t, nil, currentKey, agentStateRecord{
				Response: &savedResponse{ResponseID: "resp_older_endpoint"},
			})
			deployed := agentStateRecord{Invocation: &savedInvocation{InvocationID: "inv_newer_project"}}
			deployedID := "inv_newer_project"
			if protocol == "responses" {
				deployed = agentStateRecord{Response: &savedResponse{ResponseID: "resp_newer_project"}}
				deployedID = "resp_newer_project"
			}
			saveAgentStateForTest(t, nil, deployedKey, deployed)
			for _, operation := range []invocationOperation{invocationShow, invocationCancel} {
				t.Run(string(operation), func(t *testing.T) {
					_, _, _, err := resolveInvocationCommand(t.Context(),
						&invocationCommandFlags{agentName: "target", noPrompt: true}, operation)
					require.ErrorContains(t, err, "ambiguous")
					suggestion := azdext.WrapError(err).GetSuggestion()
					assert.Contains(t, suggestion, "--protocol")
					assert.Contains(t, suggestion, "--id")
				})
			}
			if protocol == "responses" {
				_, _, _, err := resolveInvocationCommand(t.Context(),
					&invocationCommandFlags{agentName: "target", protocol: protocol, noPrompt: true}, invocationShow)
				require.ErrorContains(t, err, "ambiguous")
				assert.Contains(t, azdext.WrapError(err).GetSuggestion(), "--id")
			}
			action, rc, id, err := resolveInvocationCommand(t.Context(), &invocationCommandFlags{
				agentName: "target", protocol: protocol, id: deployedID, noPrompt: true,
			}, invocationShow)
			require.NoError(t, err)
			defer rc.azdClient.Close()
			assert.Equal(t, protocol, action.flags.protocol)
			assert.Equal(t, deployedID, id)
			legacy := loadAgentStateForTest(t, rc.azdClient, currentKey)
			require.NotNil(t, legacy)
			require.NotNil(t, legacy.Response)
			assert.Empty(t, legacy.CurrentInvocationProtocol)
			assert.Equal(t, "resp_older_endpoint", legacy.Response.ResponseID)
			retained := loadAgentStateForTest(t, rc.azdClient, deployedKey)
			require.NotNil(t, retained)
			assert.Equal(t, deployed.Response, retained.Response)
			assert.Equal(t, deployed.Invocation, retained.Invocation)
			assert.Empty(t, retained.CurrentInvocationProtocol, "explicit selection must not rewrite legacy state")
		})
	}
}

func TestAutomaticInvokeAfterProtocolDriftSelectsSavedInvocation(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	const endpoint = "https://account.services.ai.azure.com/api/projects/project"
	projectServer := &helpersProjectServer{project: &azdext.ProjectConfig{
		Path: t.TempDir(),
		Services: map[string]*azdext.ServiceConfig{
			"target": {Name: "target", Host: AiAgentHost, AdditionalProperties: mustStruct(t, map[string]any{
				"kind": "hosted", "name": "agent",
				"protocols": []any{map[string]any{"protocol": "responses", "version": "1.0.0"}},
			})},
		},
	}}
	envServer := &testEnvironmentServiceServer{
		current: &azdext.Environment{Name: "test"},
		values: map[string]map[string]string{"test": {
			"AGENT_TARGET_NAME":                       "agent",
			"AGENT_TARGET_PROJECT_ENDPOINT":           endpoint,
			"AGENT_TARGET_RESPONSES_ENDPOINT":         endpoint + "/agents/agent/endpoint/protocols/openai/responses",
			"AGENT_TARGET_PROTOCOL_ENDPOINTS_VERSION": "1",
		}},
	}
	t.Setenv("AZD_SERVER", newInvokeRemoteContextTestAzdServer(t, projectServer, envServer))
	key := buildAgentKey(endpoint, "agent", "", false)
	require.NoError(t, newResponseStateStore(nil).Save(t.Context(), key, savedResponse{ResponseID: "resp_before_drift"}))
	reads, sends := 0, 0
	action := &InvokeAction{
		flags: &invokeFlags{name: "target", message: "hello"}, noPrompt: true, credential: responseTestCredential{},
		agentReader: func(context.Context, *remoteContext) (*agent_api.AgentObject, error) {
			reads++
			var agent agent_api.AgentObject
			err := json.Unmarshal([]byte(`{"name":"agent","agent_endpoint":`+
				`{"protocol_configuration":{"invocations":{}}},"versions":`+
				`{"latest":{"version":"7","definition":{"kind":"hosted"}}}}`), &agent)
			return &agent, err
		},
		invocationsTransport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			sends++
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "/api/projects/project/agents/agent/endpoint/protocols/invocations", r.URL.Path)
			return &http.Response{
				StatusCode: http.StatusOK, Request: r,
				Header: http.Header{
					"Content-Type": {"application/json"}, "X-Agent-Invocation-Id": {"inv_after_drift"},
				},
				Body: io.NopCloser(strings.NewReader(`{"output":"new invocation"}`)),
			}, nil
		}),
	}
	_, err := captureStdout(t, func() error { return action.Run(t.Context()) })
	require.NoError(t, err)
	require.Equal(t, 1, reads)
	require.Equal(t, 1, sends)
	for _, operation := range []invocationOperation{invocationShow, invocationCancel} {
		selected, rc, id, err := resolveInvocationCommand(t.Context(),
			&invocationCommandFlags{agentName: "target", noPrompt: true}, operation)
		require.NoError(t, err)
		assert.Equal(t, "invocations", selected.flags.protocol)
		assert.Equal(t, "inv_after_drift", id)
		assert.False(t, rc.agentRead)
		saved := loadAgentStateForTest(t, rc.azdClient, key)
		require.NotNil(t, saved)
		require.NotNil(t, saved.Response)
		require.NotNil(t, saved.Invocation)
		assert.Equal(t, "resp_before_drift", saved.Response.ResponseID)
		assert.Equal(t, "inv_after_drift", saved.Invocation.InvocationID)
		rc.azdClient.Close()
	}
	_, _, _, err = resolveInvocationCommand(t.Context(),
		&invocationCommandFlags{agentName: "target", noPrompt: true}, invocationFollow)
	require.ErrorContains(t, err, "invocations follow is not supported with the invocations protocol")
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

func TestExplicitVersionInvokePreservesLifecycleSelection(t *testing.T) {
	for _, protocol := range []string{"responses", "invocations"} {
		t.Run(protocol, func(t *testing.T) {
			endpoint, environment := newVersionedSessionTestProject(t)
			oldKey := buildAgentKey(endpoint, "agent", "2", false)
			key := buildAgentKey(endpoint, "agent", "7", false)
			currentKey := buildAgentKey(endpoint, "agent", "", false)
			saveAgentStateForTest(t, nil, oldKey, agentStateRecord{
				SessionID: "cached-v2", Response: &savedResponse{ResponseID: "resp_v2"},
				Invocation: &savedInvocation{InvocationID: "inv_v2"}, CurrentInvocationProtocol: agent_api.AgentProtocol(protocol),
			})
			saveAgentStateForTest(t, nil, key, agentStateRecord{SessionID: "cached-v7", ConversationID: "conv_v7"})
			var sends atomic.Int32
			transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodGet {
					assert.Equal(t, protocol, "invocations")
					assert.Equal(t, "/api/projects/project/agents/agent/endpoint/protocols/invocations/docs/openapi.json",
						r.URL.Path)
					return &http.Response{
						StatusCode: http.StatusOK, Request: r,
						Header: http.Header{"Content-Type": {"application/json"}},
						Body:   io.NopCloser(strings.NewReader(`{"openapi":"3.1.0","paths":{}}`)),
					}, nil
				}
				sends.Add(1)
				assert.Equal(t, http.MethodPost, r.Method)
				if protocol == "responses" {
					assert.Equal(t, "/api/projects/project/agents/agent/endpoint/protocols/openai/responses", r.URL.Path)
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						return nil, err
					}
					assert.Equal(t, "cached-v7", body["agent_session_id"])
					return &http.Response{
						StatusCode: http.StatusOK, Request: r,
						Header: http.Header{"Content-Type": {"text/event-stream"}},
						Body: io.NopCloser(strings.NewReader("event: response.created\ndata: " +
							`{"response":{"id":"resp_v7","status":"queued"}}` + "\n\n")),
					}, nil
				}
				assert.Equal(t, "/api/projects/project/agents/agent/endpoint/protocols/invocations", r.URL.Path)
				assert.Equal(t, "cached-v7", r.URL.Query().Get("agent_session_id"))
				return &http.Response{
					StatusCode: http.StatusOK, Request: r,
					Header: http.Header{
						"Content-Type": {"application/json"}, "X-Agent-Invocation-Id": {"inv_v7"},
					},
					Body: io.NopCloser(strings.NewReader(`{"output":"version 7"}`)),
				}, nil
			})
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				response, err := transport.RoundTrip(r)
				if !assert.NoError(t, err) {
					http.Error(w, "test transport failed", http.StatusInternalServerError)
					return
				}
				defer response.Body.Close()
				maps.Copy(w.Header(), response.Header)
				w.WriteHeader(response.StatusCode)
				_, err = io.Copy(w, response.Body)
				assert.NoError(t, err)
			}))
			t.Cleanup(server.Close)
			httpTransport, ok := server.Client().Transport.(*http.Transport)
			require.True(t, ok)
			httpTransport = httpTransport.Clone()
			httpTransport.TLSClientConfig.ServerName = "127.0.0.1"
			httpTransport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
				var dialer net.Dialer
				return dialer.DialContext(ctx, network, server.Listener.Addr().String())
			}
			previous := http.DefaultTransport
			http.DefaultTransport = httpTransport
			t.Cleanup(func() {
				http.DefaultTransport = previous
				httpTransport.CloseIdleConnections()
			})
			action := &InvokeAction{
				flags: &invokeFlags{
					name: "target", version: "7", protocol: protocol, message: "hello",
					longRunning: protocol == "responses", noWait: protocol == "responses",
				},
				noPrompt: true, credential: responseTestCredential{}, invocationsTransport: transport,
			}
			output, err := captureStdout(t, func() error { return action.Run(t.Context()) })
			require.NoError(t, err)
			require.EqualValues(t, 1, sends.Load())
			if protocol == "responses" {
				assert.Contains(t, output, `invocations follow --id "resp_v7" --protocol responses --agent-name "target"`)
			}
			wantID := "inv_v7"
			if protocol == "responses" {
				wantID = "resp_v7"
			}
			for _, version := range []string{"2", "9"} {
				environment.values["test"]["AGENT_TARGET_VERSION"] = version
				environment.values["test"]["AGENT_TARGET_ENDPOINT"] = endpoint + "/agents/agent/versions/" + version
				for _, operation := range []invocationOperation{invocationShow, invocationFollow, invocationCancel} {
					selected, rc, id, err := resolveInvocationCommand(t.Context(),
						&invocationCommandFlags{agentName: "target", noPrompt: true}, operation)
					if protocol == "invocations" && operation == invocationFollow {
						require.ErrorContains(t, err, "invocations follow is not supported with the invocations protocol")
						continue
					}
					require.NoError(t, err)
					require.Equal(t, protocol, selected.flags.protocol)
					require.Equal(t, wantID, id)
					require.False(t, rc.agentRead)
					rc.azdClient.Close()
				}
			}
			oldState := loadAgentStateForTest(t, nil, oldKey)
			state := loadAgentStateForTest(t, nil, key)
			require.NotNil(t, oldState)
			require.NotNil(t, state)
			assert.Equal(t, "cached-v2", oldState.SessionID)
			assert.Equal(t, "cached-v7", state.SessionID)
			accepted := loadAgentStateForTest(t, nil, currentKey)
			require.NotNil(t, accepted)
			assert.Equal(t, agent_api.AgentProtocol(protocol), accepted.CurrentInvocationProtocol)
			environment.values["test"]["AGENT_TARGET_VERSION"] = "2"
			environment.values["test"]["AGENT_TARGET_ENDPOINT"] = endpoint + "/agents/agent/versions/2"
			otherProtocol, otherID := "responses", "resp_v2"
			if protocol == "responses" {
				otherProtocol, otherID = "invocations", "inv_v2"
			}
			_, rc, id, err := resolveInvocationCommand(t.Context(),
				&invocationCommandFlags{agentName: "target", protocol: otherProtocol, noPrompt: true}, invocationShow)
			require.NoError(t, err)
			assert.Equal(t, otherID, id, "explicit protocol retains legacy work without changing current selection")
			rc.azdClient.Close()
			if protocol == "responses" {
				require.NoError(t, newResponseStateStore(nil).Delete(t.Context(), currentKey))
			} else {
				require.NoError(t, newInvocationStateStore(nil).Delete(t.Context(), currentKey))
			}
			_, _, _, err = resolveInvocationCommand(t.Context(),
				&invocationCommandFlags{agentName: "target", noPrompt: true}, invocationShow)
			require.ErrorContains(t, err, "no current invocation is saved")
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
