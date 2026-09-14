// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type trackingReadCloser struct {
	io.Reader
	closed bool
}

func (r *trackingReadCloser) Close() error {
	r.closed = true
	return nil
}

func TestInvocationIDFromResponse(t *testing.T) {
	t.Run("header", func(t *testing.T) {
		resp := &http.Response{Header: http.Header{"X-Agent-Invocation-Id": []string{"inv_header"}}}
		id, err := invocationIDFromResponse(resp)
		require.NoError(t, err)
		assert.Equal(t, "inv_header", id)
	})

	t.Run("successful body is untouched", func(t *testing.T) {
		body := `{"result":"ok"}`
		original := &trackingReadCloser{Reader: strings.NewReader(body)}
		resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: original}
		id, err := invocationIDFromResponse(resp)
		require.NoError(t, err)
		assert.Empty(t, id)
		assert.False(t, original.closed)
		remaining, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, body, string(remaining))
	})

	t.Run("accepted body is restored", func(t *testing.T) {
		body := `{"invocation_id":"inv_body","status":"accepted"}`
		original := &trackingReadCloser{Reader: strings.NewReader(body)}
		resp := &http.Response{
			StatusCode: http.StatusAccepted,
			Header:     make(http.Header),
			Body:       original,
		}
		id, err := invocationIDFromResponse(resp)
		require.NoError(t, err)
		assert.Equal(t, "inv_body", id)
		restored, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, body, string(restored))
		assert.True(t, original.closed)
	})
}

func TestInvocationsRemotePersistsCurrentID(t *testing.T) {
	const agentKey = "target-agent"
	const invocationID = "inv_created"
	for _, tt := range []struct {
		name        string
		status      int
		headerID    string
		contentType string
		body        string
		wantID      string
		wantOutput  string
	}{
		{
			name: "sync header ID", status: http.StatusOK, headerID: invocationID,
			body: `{"result":"sync-result"}`, wantID: invocationID, wantOutput: "sync-result",
		},
		{
			name: "stream header ID", status: http.StatusOK, headerID: invocationID,
			contentType: "text/event-stream", body: "data: stream-result\n\ndata: [DONE]\n\n",
			wantID: invocationID, wantOutput: "stream-result",
		},
		{
			name: "accepted body ID", status: http.StatusAccepted,
			body:   `{"invocation_id":"inv_created","status":"running","result":"accepted-result"}`,
			wantID: invocationID, wantOutput: "terminal-result",
		},
		{
			name: "HTTP failure preserves selection", status: http.StatusInternalServerError, headerID: invocationID,
			body: `{"error":{"message":"request rejected"}}`, wantID: "inv_previous",
		},
		{
			name: "missing ID preserves selection", status: http.StatusOK,
			body: `{"result":"sync-result"}`, wantID: "inv_previous", wantOutput: "sync-result",
		},
	} {
		for _, format := range []string{"default", outputRaw} {
			t.Run(tt.name+"/"+format, func(t *testing.T) {
				config := newInvokeUserConfigServer()
				config.setJSON(t, invocationsConfigPath, map[string]savedInvocation{
					agentKey: {InvocationID: "inv_previous"}, "other-agent": {InvocationID: "inv_other"},
				})
				config.setJSON(t, responsesConfigPath, map[string]savedResponse{
					agentKey: {ResponseID: "resp_previous"},
				})
				client := newInvokeTestAzdClient(t, config)
				var methods []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					methods = append(methods, r.Method)
					assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
					assert.Equal(t, "v1", r.URL.Query().Get("api-version"))
					path := "/agents/agent/endpoint/protocols/invocations"
					if r.Method == http.MethodGet {
						assert.Equal(t, http.StatusAccepted, tt.status)
						assert.Equal(t, path+"/"+invocationID, r.URL.Path)
						_, _ = io.WriteString(w,
							`{"invocation_id":"inv_created","status":"completed","result":"terminal-result"}`)
						return
					}
					assert.Equal(t, http.MethodPost, r.Method)
					assert.Equal(t, path, r.URL.Path)
					if tt.headerID != "" {
						w.Header().Set("x-agent-invocation-id", tt.headerID)
					}
					if tt.contentType != "" {
						w.Header().Set("Content-Type", tt.contentType)
					}
					w.WriteHeader(tt.status)
					_, _ = io.WriteString(w, tt.body)
				}))
				defer server.Close()
				action := &InvokeAction{
					flags:      &invokeFlags{message: `{"input":"test"}`, protocol: "invocations", outputFmt: format},
					credential: responseTestCredential{},
					// Endpoint mode avoids project-only OpenAPI caching in this HTTP/config integration test.
					endpoint: &parsedAgentEndpoint{},
					resolvedRemoteContext: &remoteContext{
						projectEndpoint: server.URL, name: "agent", serviceName: "service", apiVersion: "v1",
						agentKey: agentKey, azdClient: client,
					},
				}
				var invokeErr error
				output := withCapturedStdout(t, func() { invokeErr = action.invocationsRemote(t.Context()) })
				if tt.status >= http.StatusBadRequest {
					require.ErrorContains(t, invokeErr, "HTTP 500")
				} else {
					require.NoError(t, invokeErr)
					assert.Contains(t, output, tt.wantOutput)
				}
				wantMethods := []string{"POST"}
				if tt.status == http.StatusAccepted {
					wantMethods = append(wantMethods, "GET")
				}
				assert.Equal(t, wantMethods, methods)
				if format == outputRaw {
					assert.Contains(t, output, tt.body, "ID extraction must preserve the original response body")
					assert.NotContains(t, output, "Invocation:   ")
				}
				var saved map[string]savedInvocation
				config.getJSON(t, invocationsConfigPath, &saved)
				assert.Equal(t, map[string]savedInvocation{
					agentKey: {InvocationID: tt.wantID}, "other-agent": {InvocationID: "inv_other"},
				}, saved)
				var responses map[string]savedResponse
				config.getJSON(t, responsesConfigPath, &responses)
				assert.Equal(t, map[string]savedResponse{agentKey: {ResponseID: "resp_previous"}}, responses)
			})
		}
	}
}

func TestInvocationsProtocolDispatchHTTP(t *testing.T) {
	for _, operation := range []invocationOperation{invocationShow, invocationCancel, invocationFollow} {
		t.Run(string(operation), func(t *testing.T) {
			var methods []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				methods = append(methods, r.Method)
				assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
				assert.Equal(t, "v1", r.URL.Query().Get("api-version"))
				assert.Empty(t, r.URL.Query().Get("agent_session_id"))
				path := "/agents/agent/endpoint/protocols/invocations/inv_test"
				if r.Method == http.MethodPost {
					path += "/cancel"
				}
				assert.Equal(t, path, r.URL.Path)
				_, _ = io.WriteString(w, `{"invocation_id":"inv_test","status":"completed"}`)
			}))
			defer server.Close()
			action := &InvokeAction{flags: &invokeFlags{protocol: "invocations"}, credential: responseTestCredential{}}
			rc := &remoteContext{projectEndpoint: server.URL, name: "agent", apiVersion: "v1"}
			var output bytes.Buffer
			err := action.runInvocationOperation(t.Context(), rc, "inv_test", operation, "json", &output)
			switch operation {
			case invocationShow:
				require.NoError(t, err)
				assert.JSONEq(t, `{"invocation_id":"inv_test","status":"completed"}`, output.String())
				assert.Equal(t, []string{"GET"}, methods)
			case invocationCancel:
				require.NoError(t, err)
				assert.Contains(t, output.String(), "is completed")
				assert.Equal(t, []string{"POST"}, methods)
			case invocationFollow:
				require.ErrorContains(t, err, "not supported")
				assert.Empty(t, methods)
			}
		})
	}
}

func TestInvocationSnapshotIdentityHTTP(t *testing.T) {
	for _, tt := range []struct {
		name, header, body, wantErr string
	}{
		{name: "header only", header: "inv_test", body: `{"status":"completed","result":"done"}`},
		{name: "header overrides handler IDs", header: "inv_test",
			body: `{"id":123,"invocation_id":"handler-owned-id","status":"completed"}`},
		{name: "header mismatch even with matching body", header: "inv_other",
			body: `{"invocation_id":"inv_test","status":"completed"}`, wantErr: "does not match requested ID"},
		{name: "body invocation ID fallback", body: `{"invocation_id":"inv_test","status":"completed"}`},
		{name: "body ID fallback", body: `{"id":"inv_test","status":"completed"}`},
		{name: "body mismatch", body: `{"invocation_id":"inv_other"}`, wantErr: "does not match requested ID"},
		{name: "requested ID when no identity supplied", body: `{"status":"completed","result":"done"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/agents/agent/endpoint/protocols/invocations/inv_test", r.URL.Path)
				if tt.header != "" {
					w.Header().Set("x-agent-invocation-id", tt.header)
				}
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()
			action := &InvokeAction{flags: &invokeFlags{protocol: "invocations"}, credential: responseTestCredential{}}
			rc := &remoteContext{projectEndpoint: server.URL, name: "agent", apiVersion: "v1"}
			result, err := action.getInvocation(t.Context(), rc, "inv_test")
			assert.Equal(t, 1, calls)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.body, string(result.raw), "do not rewrite the handler's JSON")
			var output bytes.Buffer
			require.NoError(t, printInvocationSnapshot(&output, result, "table"))
			assert.Contains(t, output.String(), "Invocation ID  inv_test")
			output.Reset()
			require.NoError(t, printInvocationSnapshot(&output, result, "json"))
			assert.JSONEq(t, tt.body, output.String())
		})
	}
}

func TestInvocationCancelUnsupportedHTTP(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"cancel_invocation not implemented"}}`)
			return
		}
		w.Header().Set("x-agent-invocation-id", "inv_test")
		_, _ = io.WriteString(w, `{"status":"running"}`)
	}))
	defer server.Close()
	action := &InvokeAction{flags: &invokeFlags{protocol: "invocations"}, credential: responseTestCredential{}}
	rc := &remoteContext{projectEndpoint: server.URL, name: "agent", apiVersion: "v1"}
	err := action.runInvocationOperation(t.Context(), rc, "inv_test", invocationCancel, "", io.Discard)
	require.EqualError(t, err, "This agent does not support cancelling invocations.")
	serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
	require.True(t, ok)
	assert.Equal(t, http.StatusNotFound, serviceErr.StatusCode)
	assert.NotEmpty(t, serviceErr.ServiceName)
	assert.Equal(t, []string{"POST", "GET"}, methods)
}

func TestInvocationCancelTerminalFallbackHTTP(t *testing.T) {
	for _, tt := range []struct {
		name       string
		status     string
		getStatus  int
		headerID   string
		wantNoWork bool
	}{
		{name: "completed", status: "completed", wantNoWork: true},
		{name: "failed", status: "failed", wantNoWork: true},
		{name: "cancelled", status: "cancelled", wantNoWork: true},
		{name: "canceled alias", status: "canceled", wantNoWork: true},
		{name: "still active", status: "running"},
		{name: "GET failed", getStatus: http.StatusNotFound},
		{name: "terminal response for wrong ID", status: "completed", headerID: "inv_other"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var methods []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				methods = append(methods, r.Method)
				assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
				assert.Equal(t, "v1", r.URL.Query().Get("api-version"))
				assert.Empty(t, r.URL.Query().Get("agent_session_id"))
				path := "/agents/agent/endpoint/protocols/invocations/inv_test"
				if r.Method == http.MethodPost {
					assert.Equal(t, path+"/cancel", r.URL.Path)
					w.WriteHeader(http.StatusConflict)
					_, _ = io.WriteString(w, `{"error":{"message":"cancellation rejected"}}`)
					return
				}
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, path, r.URL.Path)
				if tt.getStatus != 0 {
					w.WriteHeader(tt.getStatus)
					return
				}
				headerID := tt.headerID
				if headerID == "" {
					headerID = "inv_test"
				}
				w.Header().Set("x-agent-invocation-id", headerID)
				_, _ = io.WriteString(w, `{"status":"`+tt.status+`"}`)
			}))
			defer server.Close()
			action := &InvokeAction{flags: &invokeFlags{protocol: "invocations"}, credential: responseTestCredential{}}
			rc := &remoteContext{projectEndpoint: server.URL, name: "agent", apiVersion: "v1"}
			var output bytes.Buffer
			err := action.runInvocationOperation(t.Context(), rc, "inv_test", invocationCancel, "", &output)
			assert.Equal(t, []string{"POST", "GET"}, methods)
			if tt.wantNoWork {
				require.NoError(t, err)
				assert.Equal(t, "Invocation inv_test is already "+tt.status+"; nothing to cancel.\n", output.String())
			} else {
				require.Error(t, err)
				serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
				require.True(t, ok)
				assert.Equal(t, http.StatusConflict, serviceErr.StatusCode, "preserve the original cancel rejection")
				assert.Empty(t, output.String())
			}
		})
	}
}

func TestInvocationErrorDetailRemainsBounded(t *testing.T) {
	for _, body := range []string{
		`{"error":{"code":"not_found","message":"private handler content"}}`,
		`not JSON`,
		`{"error":{"code":"not_found","message":"cancel_invocation not implemented"},"padding":"` +
			strings.Repeat("x", 16*1024) + `"}`,
	} {
		err := classifyInvocationLifecycleError(&invocationLifecycleHTTPError{
			method: http.MethodPost, requestURL: "https://example.test/invocations/inv_test/cancel",
			statusCode: http.StatusNotFound, status: "404 Not Found", body: []byte(body),
		}, exterrors.OpCancelInvocation, "cancelling Invocation")
		require.EqualError(t, err, "cancelling Invocation failed with HTTP 404: 404 Not Found")
	}
}

func TestInvocationLifecycleURLs(t *testing.T) {
	assert.Equal(
		t,
		"https://example.test/agents/agent/endpoint/protocols/invocations/inv_1?api-version=v1",
		buildInvocationLifecycleURL("https://example.test", "agent", "inv_1", "v1"),
	)
	assert.Equal(
		t,
		"https://example.test/agents/agent/endpoint/protocols/invocations/inv_1/cancel?api-version=v1",
		buildInvocationCancelURL("https://example.test", "agent", "inv_1", "v1"),
	)
}

func TestPrintInvocationSnapshot(t *testing.T) {
	var output bytes.Buffer
	require.NoError(t, printInvocationSnapshot(&output, invocationSnapshotResult{
		snapshot: invocationSnapshot{ID: "inv_1", Status: "completed"},
		raw:      []byte(`{"id":"inv_1","status":"completed"}`),
	}, "json"))
	assert.JSONEq(t, `{"id":"inv_1","status":"completed"}`, output.String())

	output.Reset()
	require.NoError(t, printInvocationSnapshot(&output, invocationSnapshotResult{
		snapshot: invocationSnapshot{ID: "inv_1", Status: "completed"},
	}, "table"))
	assert.Contains(t, output.String(), "Invocation ID  inv_1")
	assert.Contains(t, output.String(), "Status         completed")
}

func TestInvocationStateStoreRoundTrip(t *testing.T) {
	server := newInvokeUserConfigServer()
	client := newInvokeTestAzdClient(t, server)
	store := newInvocationStateStore(client)
	want := savedInvocation{InvocationID: "inv_123"}

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
