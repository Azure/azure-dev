// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
