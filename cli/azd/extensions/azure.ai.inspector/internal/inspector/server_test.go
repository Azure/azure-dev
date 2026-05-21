// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package inspector

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestServerStartServesIndex asserts /, /index.html, and unknown SPA
// routes all return the embedded index.html.
func TestServerStartServesIndex(t *testing.T) {
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
		t.Fatal("server did not become ready in time")
	}

	for _, path := range []string{"/", "/index.html", "/some/spa/route"} {
		t.Run(path, func(t *testing.T) {
			resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(port) + path)
			if err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s: status = %d, want 200", path, resp.StatusCode)
			}
			body, _ := io.ReadAll(resp.Body)
			if !strings.Contains(strings.ToLower(string(body)), "<html") {
				t.Fatalf("GET %s: body does not look like HTML (first 80 bytes: %q)", path, truncate(body, 80))
			}
		})
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not shut down in time")
	}
}

func TestWebSocketOriginValidationRejectsRebindingHost(t *testing.T) {
	port := pickFreePort(t)
	srv := New(Config{Port: port, AgentPort: 8088})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- srv.Start(ctx, ready) }()

	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not become ready in time")
	}

	wsURL := url.URL{Scheme: "ws", Host: "127.0.0.1:" + strconv.Itoa(port), Path: "/agentdev/ws/rpc"}

	validHeaders := http.Header{"Origin": []string{"http://127.0.0.1:" + strconv.Itoa(port)}}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL.String(), validHeaders)
	if err != nil {
		t.Fatalf("valid websocket origin should connect: %v", err)
	}
	_ = conn.Close()

	invalidHeaders := http.Header{"Origin": []string{"http://evil.example:" + strconv.Itoa(port)}}
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL.String(), invalidHeaders)
	if err == nil {
		_ = conn.Close()
		t.Fatal("rebinding websocket origin should be rejected")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("rejected websocket status = %v, want 403", resp)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not shut down in time")
	}
}

func pickFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n])
}
