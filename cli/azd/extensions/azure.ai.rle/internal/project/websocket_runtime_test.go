// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/gorilla/websocket"
)

func TestRunWebSocketShellUsesPersistentSocketForStatefulOperations(t *testing.T) {
	var requests []map[string]any
	var safePaths []string
	upgrades := 0
	var captureMu sync.Mutex
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api-version") != "test-version" {
			t.Errorf("expected preserved API version, got %q", r.URL.Query().Get("api-version"))
		}
		if r.Header.Get("Authorization") != "******" {
			t.Errorf("unexpected authorization header %q", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/ws" {
			captureMu.Lock()
			safePaths = append(safePaths, r.URL.Path)
			captureMu.Unlock()
			_, _ = fmt.Fprintf(w, `{"path":%q}`, r.URL.Path) //nolint:gosec // Test response uses JSON encoding.
			return
		}

		captureMu.Lock()
		upgrades++
		captureMu.Unlock()
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
			captureMu.Lock()
			requests = append(requests, request)
			captureMu.Unlock()
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

	captureMu.Lock()
	capturedUpgrades := upgrades
	capturedRequests := slices.Clone(requests)
	capturedSafePaths := slices.Clone(safePaths)
	captureMu.Unlock()
	if capturedUpgrades != 1 {
		t.Fatalf("expected one persistent WebSocket, got %d", capturedUpgrades)
	}
	if len(capturedRequests) != 3 {
		t.Fatalf("expected three WebSocket requests, got %#v", capturedRequests)
	}
	assertWebSocketRequest(t, capturedRequests[0], "reset", map[string]any{"seed": float64(42)})
	assertWebSocketRequest(t, capturedRequests[1], "step", map[string]any{"message": "hello"})
	assertWebSocketRequest(t, capturedRequests[2], "state", nil)
	if !slices.Equal(capturedSafePaths, []string{"/health", "/metadata", "/schema"}) {
		t.Fatalf("unexpected safe HTTP operations: %v", capturedSafePaths)
	}
	if authorizationCalls != 4 {
		t.Fatalf("expected one WebSocket and three HTTP authorization calls, got %d", authorizationCalls)
	}
	if !strings.Contains(output.String(), `"requestType": "step"`) {
		t.Fatalf("expected formatted WebSocket response, got %s", output.String())
	}
}

func TestWebSocketRuntimeSessionRestartUsesReplacementRuntime(t *testing.T) {
	newServer := func(t *testing.T) *httptest.Server {
		t.Helper()
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			connection, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("upgrade WebSocket: %v", err)
				return
			}
			defer connection.Close()
			var request map[string]any
			if err := connection.ReadJSON(&request); err != nil {
				return
			}
			_ = connection.WriteJSON(map[string]any{
				"type": request["type"],
				"data": map[string]any{"runtime": r.Host},
			})
		}))
	}

	first := newServer(t)
	defer first.Close()
	second := newServer(t)
	defer second.Close()

	session := NewWebSocketRuntimeSession(first.URL, 30, nil)
	defer session.Close()
	firstResponse, err := session.Call(t.Context(), "state", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(firstResponse, first.URL[strings.Index(first.URL, "://")+3:]) {
		t.Fatalf("expected first runtime response, got %s", firstResponse)
	}

	session.Restart(second.URL)
	secondResponse, err := session.Call(t.Context(), "state", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(secondResponse, second.URL[strings.Index(second.URL, "://")+3:]) {
		t.Fatalf("expected replacement runtime response, got %s", secondResponse)
	}
}

func TestWebSocketHandshakeRetriesTransientFailures(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) < 3 {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		connection, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade WebSocket: %v", err)
			return
		}
		defer connection.Close()
		if _, _, err := connection.ReadMessage(); err != nil {
			return
		}
		if err := connection.WriteJSON(map[string]any{"type": "state", "data": map[string]any{}}); err != nil {
			t.Errorf("write WebSocket response: %v", err)
		}
	}))
	defer server.Close()

	session := NewWebSocketRuntimeSession(server.URL, 30, nil)
	session.handshakeRetryDelay = func(int) (time.Duration, error) { return time.Millisecond, nil }
	defer session.Close()
	if _, err := session.Call(t.Context(), "state", ""); err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 3 {
		t.Fatalf("expected three handshake attempts, got %d", attempts.Load())
	}
}

func TestWebSocketHandshakeUsesConfiguredRetryPolicy(t *testing.T) {
	session := NewWebSocketRuntimeSession("https://example.test", 60, nil)

	if session.connectionTimeout != 90*time.Second {
		t.Fatalf("expected 90-second connection budget, got %s", session.connectionTimeout)
	}
	if session.handshakeTimeout != 25*time.Second {
		t.Fatalf("expected 25-second handshake timeout, got %s", session.handshakeTimeout)
	}
	if session.handshakeMaxAttempts != 3 {
		t.Fatalf("expected three handshake attempts, got %d", session.handshakeMaxAttempts)
	}
}

func TestWebSocketHandshakeDoesNotRetryPermanentFailure(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	session := NewWebSocketRuntimeSession(server.URL, 30, nil)
	session.handshakeRetryDelay = func(int) (time.Duration, error) { return time.Millisecond, nil }
	defer session.Close()
	if _, err := session.Call(t.Context(), "state", ""); err == nil {
		t.Fatal("expected WebSocket handshake to fail")
	}
	if attempts.Load() != 1 {
		t.Fatalf("expected one handshake attempt, got %d", attempts.Load())
	}
}

func TestWebSocketHandshakeStopsAfterRetryLimit(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	session := NewWebSocketRuntimeSession(server.URL, 30, nil)
	session.handshakeRetryDelay = func(int) (time.Duration, error) { return 0, nil }
	defer session.Close()
	if _, err := session.Call(t.Context(), "state", ""); err == nil {
		t.Fatal("expected WebSocket handshake to fail")
	}
	if attempts.Load() != webSocketHandshakeMaxAttempts {
		t.Fatalf("expected %d handshake attempts, got %d", webSocketHandshakeMaxAttempts, attempts.Load())
	}
}

func TestWebSocketHandshakeSurfacesJitterFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	session := NewWebSocketRuntimeSession(server.URL, 30, nil)
	session.handshakeRetryDelay = func(int) (time.Duration, error) {
		return 0, errors.New("entropy unavailable")
	}
	defer session.Close()

	_, err := session.Call(t.Context(), "state", "")
	if err == nil || !strings.Contains(err.Error(), "calculate WebSocket handshake retry delay") {
		t.Fatalf("expected jitter failure, got %v", err)
	}
}

func TestWebSocketHandshakeCancellationStopsBackoff(t *testing.T) {
	attemptReceived := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-attemptReceived:
		default:
			close(attemptReceived)
		}
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	session := NewWebSocketRuntimeSession(server.URL, 30, nil)
	session.connectionTimeout = 2 * time.Hour
	session.handshakeRetryDelay = func(int) (time.Duration, error) { return time.Hour, nil }
	defer session.Close()
	ctx, cancel := context.WithCancel(t.Context())
	callDone := make(chan error, 1)
	go func() {
		_, err := session.Call(ctx, "state", "")
		callDone <- err
	}()
	<-attemptReceived
	cancel()
	select {
	case err := <-callDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("expected cancellation to stop handshake backoff")
	}
}

func TestWebSocketHandshakePreservesCallerDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	session := NewWebSocketRuntimeSession(server.URL, 30, nil)
	session.handshakeTimeout = time.Second
	defer session.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()

	if _, err := session.Call(ctx, "state", ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected caller deadline, got %v", err)
	}
}

func TestWebSocketConnectionBudgetIncludesAuthorization(t *testing.T) {
	authorizationProvider := func(ctx context.Context) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	session := NewWebSocketRuntimeSession("https://example.test", 30, authorizationProvider)
	session.connectionTimeout = 30 * time.Millisecond
	defer session.Close()

	started := time.Now()
	if _, err := session.Call(t.Context(), "state", ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected connection deadline, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("expected authorization to share the connection budget, took %s", elapsed)
	}
}

func TestWebSocketOperationTimeoutStartsAfterHandshake(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			time.Sleep(600 * time.Millisecond)
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		connection, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade WebSocket: %v", err)
			return
		}
		defer connection.Close()
		if _, _, err := connection.ReadMessage(); err != nil {
			return
		}
		time.Sleep(500 * time.Millisecond)
		_ = connection.WriteJSON(map[string]any{"type": "state", "data": map[string]any{}})
	}))
	defer server.Close()

	session := NewWebSocketRuntimeSession(server.URL, 1, nil)
	session.connectionTimeout = 2 * time.Second
	session.handshakeRetryDelay = func(int) (time.Duration, error) { return time.Millisecond, nil }
	defer session.Close()
	started := time.Now()
	if _, err := session.Call(t.Context(), "state", ""); err != nil {
		t.Fatalf("expected operation timeout to start after connection: %v", err)
	}
	if elapsed := time.Since(started); elapsed < time.Second {
		t.Fatalf("expected test to include handshake and operation time, took %s", elapsed)
	}
}

func TestWebSocketHandshakeUsesOverallConnectionBudget(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		<-r.Context().Done()
	}))
	defer server.Close()

	session := NewWebSocketRuntimeSession(server.URL, 30, nil)
	session.connectionTimeout = 50 * time.Millisecond
	session.handshakeTimeout = time.Second
	session.handshakeRetryDelay = func(int) (time.Duration, error) { return 0, nil }
	defer session.Close()

	started := time.Now()
	if _, err := session.Call(t.Context(), "state", ""); err == nil {
		t.Fatal("expected connection budget to expire")
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("expected overall connection budget to stop handshakes, took %s", elapsed)
	}
	if attempts.Load() != 1 {
		t.Fatalf("expected one budget-consuming attempt, got %d", attempts.Load())
	}
}

func TestWebSocketHandshakeUsesPerAttemptTimeout(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		<-r.Context().Done()
	}))
	defer server.Close()

	session := NewWebSocketRuntimeSession(server.URL, 30, nil)
	session.connectionTimeout = time.Second
	session.handshakeTimeout = 40 * time.Millisecond
	session.handshakeMaxAttempts = 2
	session.handshakeRetryDelay = func(int) (time.Duration, error) { return 0, nil }
	defer session.Close()

	started := time.Now()
	if _, err := session.Call(t.Context(), "state", ""); err == nil {
		t.Fatal("expected handshake attempts to time out")
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("expected per-attempt timeouts to bound retries, took %s", elapsed)
	}
	if attempts.Load() != 2 {
		t.Fatalf("expected two handshake attempts, got %d", attempts.Load())
	}
}

func TestWebSocketHandshakeRetryBackoffCeilings(t *testing.T) {
	expected := []time.Duration{2 * time.Second, 4 * time.Second, 4 * time.Second}
	for retry, expectedCeiling := range expected {
		if ceiling := webSocketHandshakeRetryCeiling(retry); ceiling != expectedCeiling {
			t.Errorf("retry %d: expected ceiling %s, got %s", retry, expectedCeiling, ceiling)
		}
		for range 100 {
			delay, err := webSocketHandshakeRetryDelay(retry)
			if err != nil {
				t.Fatal(err)
			}
			if delay < 0 || delay > expectedCeiling {
				t.Fatalf("retry %d: full-jitter delay %s exceeds [0, %s]", retry, delay, expectedCeiling)
			}
		}
	}
}

func TestRetryableWebSocketHandshakeStatuses(t *testing.T) {
	retryable := []int{408, 429, 500, 502, 503, 504}
	for _, statusCode := range retryable {
		if !isRetryableWebSocketHandshakeStatus(statusCode) {
			t.Errorf("expected status %d to be retryable", statusCode)
		}
	}
	for _, statusCode := range []int{400, 401, 403, 404} {
		if isRetryableWebSocketHandshakeStatus(statusCode) {
			t.Errorf("expected status %d not to be retryable", statusCode)
		}
	}
}

func TestRetryableWebSocketHandshakeErrors(t *testing.T) {
	if !isRetryableWebSocketHandshakeError(&net.DNSError{Err: "temporary failure", IsTemporary: true}) {
		t.Fatal("expected network error to be retryable")
	}
	if isRetryableWebSocketHandshakeError(errors.New("invalid proxy configuration")) {
		t.Fatal("expected local configuration error not to be retryable")
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

func TestWebSocketSessionKeepaliveExchangesStateAndProcessesServerPing(t *testing.T) {
	keepAliveReceived := make(chan struct{}, 1)
	pongReceived := make(chan struct{}, 1)
	serverDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(serverDone)
		connection, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade WebSocket: %v", err)
			return
		}
		defer connection.Close()
		connection.SetPongHandler(func(string) error {
			select {
			case pongReceived <- struct{}{}:
			default:
			}
			return nil
		})
		requestCount := 0
		for {
			var request map[string]any
			if err := connection.ReadJSON(&request); err != nil {
				return
			}
			requestCount++
			if requestCount == 1 {
				if err := connection.WriteControl(
					websocket.PingMessage,
					nil,
					time.Now().Add(time.Second),
				); err != nil {
					t.Errorf("write WebSocket ping: %v", err)
					return
				}
			} else {
				select {
				case keepAliveReceived <- struct{}{}:
				default:
				}
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
	case <-keepAliveReceived:
	case <-time.After(time.Second):
		t.Fatal("expected WebSocket state keepalive")
	}
	select {
	case <-pongReceived:
	case <-time.After(time.Second):
		t.Fatal("expected client to process and answer the server ping")
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
			!strings.Contains(localError.Suggestion, "run rollout again") {
			t.Fatalf("expected %s to be terminal with rollout-again guidance: terminal=%t err=%v", code, terminal, err)
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

func TestCallAndDrainHasNoImplicitTimeoutWhenDisabled(t *testing.T) {
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
		time.Sleep(25 * time.Millisecond)
		if err := connection.WriteJSON(map[string]any{"type": "state", "data": map[string]any{}}); err != nil {
			t.Errorf("write WebSocket response: %v", err)
		}
	}))
	defer server.Close()

	session := NewWebSocketRuntimeSession(server.URL, 0, nil)
	defer session.Close()
	if _, err := session.CallAndDrain(t.Context(), "state", ""); err != nil {
		t.Fatalf("expected timeout-disabled call to wait for its response: %v", err)
	}
}

func TestCallAndDrainBoundsResponseDrainAfterCancellation(t *testing.T) {
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
	ctx, cancel := context.WithCancel(t.Context())
	callDone := make(chan error, 1)
	go func() {
		_, err := session.CallAndDrain(ctx, "state", "")
		callDone <- err
	}()
	<-requestReceived
	cancel()
	select {
	case err := <-callDone:
		if err == nil {
			t.Fatal("expected response drain to end after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("expected cancellation to bound the response drain")
	}
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
