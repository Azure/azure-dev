// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/gorilla/websocket"
)

func TestRunWebSocketShellUsesPersistentSocketForStatefulOperations(t *testing.T) {
	var requests []map[string]any
	var safePaths []string
	upgrades := 0
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api-version") != "test-version" {
			t.Errorf("expected preserved API version, got %q", r.URL.Query().Get("api-version"))
		}
		if r.Header.Get("Authorization") != "******" {
			t.Errorf("unexpected authorization header %q", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/ws" {
			safePaths = append(safePaths, r.URL.Path)
			_, _ = fmt.Fprintf(w, `{"path":%q}`, r.URL.Path) //nolint:gosec // Test response uses JSON encoding.
			return
		}

		upgrades++
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade WebSocket: %v", err)
			return
		}
		defer connection.Close()
		for {
			var request map[string]any
			if err := connection.ReadJSON(&request); err != nil {
				return
			}
			requests = append(requests, request)
			responseType := "observation"
			if request["type"] == "state" {
				responseType = "state"
			}
			if err := connection.WriteJSON(map[string]any{
				"type": responseType,
				"data": map[string]any{"requestType": request["type"]},
			}); err != nil {
				t.Errorf("write WebSocket response: %v", err)
				return
			}
		}
	}))
	defer server.Close()

	authorizationCalls := 0
	authorizationProvider := func(context.Context) (string, error) {
		authorizationCalls++
		return "******", nil
	}
	input := strings.NewReader(
		"reset {\"seed\":42}\nstep {\"message\":\"hello\"}\nstate\nhealth\nmetadata\nschema\nexit\n",
	)
	var output bytes.Buffer
	err := RunWebSocketShellWithContextAndAuthorizationProvider(
		t.Context(),
		input,
		&output,
		server.URL+"?api-version=test-version",
		30,
		authorizationProvider,
	)
	if err != nil {
		t.Fatal(err)
	}

	if upgrades != 1 {
		t.Fatalf("expected one persistent WebSocket, got %d", upgrades)
	}
	if len(requests) != 3 {
		t.Fatalf("expected three WebSocket requests, got %#v", requests)
	}
	assertWebSocketRequest(t, requests[0], "reset", map[string]any{"seed": float64(42)})
	assertWebSocketRequest(t, requests[1], "step", map[string]any{"message": "hello"})
	assertWebSocketRequest(t, requests[2], "state", nil)
	if !slices.Equal(safePaths, []string{"/health", "/metadata", "/schema"}) {
		t.Fatalf("unexpected safe HTTP operations: %v", safePaths)
	}
	if authorizationCalls != 4 {
		t.Fatalf("expected one WebSocket and three HTTP authorization calls, got %d", authorizationCalls)
	}
	if !strings.Contains(output.String(), `"requestType": "step"`) {
		t.Fatalf("expected formatted WebSocket response, got %s", output.String())
	}
}

func TestRuntimeWebSocketURL(t *testing.T) {
	tests := []struct {
		baseURL string
		want    string
	}{
		{
			baseURL: "https://example.test/openenv?api-version=1",
			want:    "wss://example.test/openenv/ws?api-version=1",
		},
		{
			baseURL: "http://127.0.0.1:8080/openenv/",
			want:    "ws://127.0.0.1:8080/openenv/ws",
		},
	}
	for _, test := range tests {
		got, err := RuntimeWebSocketURL(test.baseURL)
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Fatalf("RuntimeWebSocketURL(%q) = %q, want %q", test.baseURL, got, test.want)
		}
	}
	if _, err := RuntimeWebSocketURL("ftp://example.test/openenv"); err == nil {
		t.Fatal("expected unsupported scheme to fail")
	}
}

func TestWebSocketSessionSendsKeepalivePings(t *testing.T) {
	pingReceived := make(chan struct{}, 1)
	serverDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(serverDone)
		connection, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade WebSocket: %v", err)
			return
		}
		defer connection.Close()
		connection.SetPingHandler(func(data string) error {
			select {
			case pingReceived <- struct{}{}:
			default:
			}
			return connection.WriteControl(
				websocket.PongMessage,
				[]byte(data),
				time.Now().Add(time.Second),
			)
		})
		for {
			var request map[string]any
			if err := connection.ReadJSON(&request); err != nil {
				return
			}
			if err := connection.WriteJSON(map[string]any{
				"type": "state",
				"data": map[string]any{},
			}); err != nil {
				t.Errorf("write WebSocket response: %v", err)
				return
			}
		}
	}))
	defer server.Close()

	session := NewWebSocketRuntimeSession(server.URL, 30, nil)
	session.keepAliveInterval = 10 * time.Millisecond
	if _, err := session.Call(t.Context(), "state", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-pingReceived:
	case <-time.After(time.Second):
		t.Fatal("expected WebSocket keepalive ping")
	}
	session.Close()
	<-serverDone
}

func TestWebSocketRequestUsesOpenEnvProtocol(t *testing.T) {
	reset, err := webSocketRequest("reset", &callOptions{})
	if err != nil {
		t.Fatal(err)
	}

	step, err := webSocketRequest("step", &callOptions{action: `{"message":"hello"}`})
	if err != nil {
		t.Fatal(err)
	}
	state, err := webSocketRequest("state", &callOptions{})
	if err != nil {
		t.Fatal(err)
	}

	assertJSONEqual(t, reset, `{"type":"reset","data":{}}`)
	assertJSONEqual(t, step, `{"type":"step","data":{"message":"hello"}}`)
	assertJSONEqual(t, state, `{"type":"state"}`)
}

func TestParseWebSocketResponse(t *testing.T) {
	result, terminal, err := parseWebSocketResponse(
		"step",
		[]byte(`{"type":"observation","data":{"reward":1}}`),
	)
	if err != nil || terminal || !strings.Contains(result, `"reward": 1`) {
		t.Fatalf("unexpected successful response: result=%q terminal=%t err=%v", result, terminal, err)
	}

	_, terminal, err = parseWebSocketResponse(
		"step",
		[]byte(`{"type":"error","data":{"detail":"invalid action"}}`),
	)
	if err == nil || terminal || !strings.Contains(err.Error(), "invalid action") {
		t.Fatalf("unexpected OpenEnv error response: terminal=%t err=%v", terminal, err)
	}

	for _, code := range []string{"CAPACITY_REACHED", "FACTORY_ERROR", "SESSION_ERROR"} {
		_, terminal, err = parseWebSocketResponse(
			"step",
			fmt.Appendf(nil, `{"type":"error","data":{"code":%q,"detail":"failed"}}`, code),
		)
		localError, ok := errors.AsType[*azdext.LocalError](err)
		if err == nil || !terminal || !ok ||
			!strings.Contains(localError.Suggestion, "run invoke again") {
			t.Fatalf("expected %s to be terminal with reinvoke guidance: terminal=%t err=%v", code, terminal, err)
		}
	}

	_, terminal, err = parseWebSocketResponse(
		"step",
		[]byte(`{"type":"error","data":{"code":"VALIDATION_ERROR","detail":"invalid action"}}`),
	)
	localError, ok := errors.AsType[*azdext.LocalError](err)
	if err == nil || terminal || !ok ||
		!strings.Contains(localError.Suggestion, "payload and retry") {
		t.Fatalf("expected validation error to be recoverable: terminal=%t err=%v", terminal, err)
	}

	_, terminal, err = parseWebSocketResponse(
		"state",
		[]byte(`{"type":"observation","data":{}}`),
	)
	if err == nil || !terminal {
		t.Fatalf("expected mismatched response type to be terminal, got terminal=%t err=%v", terminal, err)
	}
}

func TestWebSocketSessionErrorPreventsReconnect(t *testing.T) {
	upgrades := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrades++
		connection, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.Close()
		if _, _, err := connection.ReadMessage(); err != nil {
			t.Error(err)
			return
		}
		if err := connection.WriteMessage(
			websocket.TextMessage,
			[]byte(`{"type":"error","data":{"code":"SESSION_ERROR","detail":"session failed"}}`),
		); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()

	session := NewWebSocketRuntimeSession(server.URL, 30, nil)
	defer session.Close()
	if _, err := session.Call(t.Context(), "state", ""); err == nil {
		t.Fatal("expected the session error to fail the first call")
	}
	if _, err := session.Call(t.Context(), "state", ""); err == nil {
		t.Fatal("expected the terminal session error to fail the second call")
	}
	if upgrades != 1 {
		t.Fatalf("expected no reconnection after session error, got %d connections", upgrades)
	}
}

func TestWebSocketSessionCloseInterruptsCallAndPreventsReconnect(t *testing.T) {
	requestReceived := make(chan struct{})
	upgrades := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrades++
		connection, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade WebSocket: %v", err)
			return
		}

		defer connection.Close()
		if _, _, err := connection.ReadMessage(); err != nil {
			t.Errorf("read WebSocket request: %v", err)
			return
		}
		close(requestReceived)
		_, _, _ = connection.ReadMessage()
	}))
	defer server.Close()

	session := NewWebSocketRuntimeSession(server.URL, 30, nil)
	callDone := make(chan error, 1)
	go func() {
		_, err := session.Call(t.Context(), "state", "")
		callDone <- err
	}()
	<-requestReceived
	session.Close()
	if err := <-callDone; err == nil {
		t.Fatal("expected closing the session to fail the active call")
	}
	if _, err := session.Call(t.Context(), "state", ""); err == nil {
		t.Fatal("expected a closed session to reject subsequent calls")
	}
	if upgrades != 1 {
		t.Fatalf("expected no reconnection after close, got %d connections", upgrades)
	}
}

func TestCanceledQueuedCallDoesNotReachWebSocket(t *testing.T) {
	firstRequestReceived := make(chan struct{})
	releaseFirstRequest := make(chan struct{})
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade WebSocket: %v", err)
			return
		}
		defer connection.Close()
		for {
			var request map[string]any
			if err := connection.ReadJSON(&request); err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
					return
				}
				t.Errorf("read WebSocket request: %v", err)
				return
			}
			requests++
			if requests == 1 {
				close(firstRequestReceived)
				<-releaseFirstRequest
			}
			if err := connection.WriteJSON(map[string]any{
				"type": "state",
				"data": map[string]any{"request": requests},
			}); err != nil {
				t.Errorf("write WebSocket response: %v", err)
				return
			}
		}
	}))
	defer server.Close()

	session := NewWebSocketRuntimeSession(server.URL, 30, nil)
	defer session.Close()
	firstCallDone := make(chan error, 1)
	go func() {
		_, err := session.Call(t.Context(), "state", "")
		firstCallDone <- err
	}()
	<-firstRequestReceived

	queuedContext, cancelQueued := context.WithCancel(t.Context())
	queuedCallDone := make(chan error, 1)
	go func() {
		_, err := session.CallAndDrain(queuedContext, "state", "")
		queuedCallDone <- err
	}()
	cancelQueued()
	close(releaseFirstRequest)

	if err := <-firstCallDone; err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	if err := <-queuedCallDone; err == nil {
		t.Fatal("expected canceled queued call to fail")
	}
	if requests != 1 {
		t.Fatalf("expected canceled queued call not to reach WebSocket, got %d requests", requests)
	}
}

func TestCallAndDrainUsesBoundedReadWhenTimeoutDisabled(t *testing.T) {
	requestReceived := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade WebSocket: %v", err)
			return
		}
		defer connection.Close()
		if _, _, err := connection.ReadMessage(); err != nil {
			t.Errorf("read WebSocket request: %v", err)
			return
		}
		close(requestReceived)
		_, _, _ = connection.ReadMessage()
	}))
	defer server.Close()

	session := NewWebSocketRuntimeSession(server.URL, 0, nil)
	session.drainTimeout = 10 * time.Millisecond
	defer session.Close()
	_, err := session.CallAndDrain(t.Context(), "state", "")
	if err == nil {
		t.Fatal("expected bounded drain to fail when the runtime does not respond")
	}
	<-requestReceived
}

func assertWebSocketRequest(
	t *testing.T,
	request map[string]any,
	requestType string,
	data map[string]any,
) {
	t.Helper()
	if request["type"] != requestType {
		t.Fatalf("request type = %#v, want %q", request["type"], requestType)
	}
	if data == nil {
		if _, ok := request["data"]; ok {
			t.Fatalf("did not expect data in %#v", request)
		}
		return
	}
	actual, ok := request["data"].(map[string]any)
	if !ok || !mapsEqual(actual, data) {
		t.Fatalf("request data = %#v, want %#v", request["data"], data)
	}
}

func assertJSONEqual(t *testing.T, actual []byte, expected string) {
	t.Helper()
	var actualValue any
	var expectedValue any
	if err := json.Unmarshal(actual, &actualValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(expected), &expectedValue); err != nil {
		t.Fatal(err)
	}
	actualJSON, _ := json.Marshal(actualValue)
	expectedJSON, _ := json.Marshal(expectedValue)
	if !bytes.Equal(actualJSON, expectedJSON) {
		t.Fatalf("JSON = %s, want %s", actual, expected)
	}
}

func mapsEqual(left map[string]any, right map[string]any) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return bytes.Equal(leftJSON, rightJSON)
}
