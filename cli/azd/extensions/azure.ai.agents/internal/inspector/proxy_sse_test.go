// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package inspector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestSSECancelAbortsUpstreamOnWSClose verifies that closing the inspector
// WebSocket while a proxyFetchSSE stream is in flight aborts the upstream
// HTTP request within ~1s. Without ctx propagation through cleanup() ->
// rootCancel() -> stream cancel -> http request ctx, the upstream would
// hang until the agent gives up on its own.
func TestSSECancelAbortsUpstreamOnWSClose(t *testing.T) {
	upstreamCancelled := make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		// Trickle one chunk so the SPA-side "stream open" path is real,
		// then block on ctx until the request is aborted.
		_, _ = w.Write([]byte("data: hello\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
		<-r.Context().Done()
		select {
		case upstreamCancelled <- struct{}{}:
		default:
		}
	}))
	defer upstream.Close()

	port := pickFreePort(t)
	srv := New(Config{Port: port})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- srv.Start(ctx, ready) }()
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not become ready")
	}

	wsURL := url.URL{Scheme: "ws", Host: "127.0.0.1:" + strconv.Itoa(port), Path: "/agentdev/ws/rpc"}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "webviewProxy/fetchSSE",
		"params": []any{map[string]any{
			"requestId": "test-1",
			"url":       upstream.URL,
			"method":    http.MethodGet,
		}},
	}
	raw, _ := json.Marshal(req)
	if err := conn.WriteMessage(websocket.TextMessage, raw); err != nil {
		t.Fatalf("send fetchSSE: %v", err)
	}

	// Wait for the first chunk so we know the upstream request is live and
	// blocked on r.Context().Done(); then close the WS to trigger cleanup.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read first chunk: %v (no SSE chunk arrived)", err)
		}
		t.Logf("ws msg: %s", string(msg))
		var env struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(msg, &env)
		if env.Method == "webviewProxy/fetchSSE/chunk" {
			break
		}
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("close ws: %v", err)
	}

	select {
	case <-upstreamCancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream request was not cancelled within 2s of WS close")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not shut down")
	}
}
