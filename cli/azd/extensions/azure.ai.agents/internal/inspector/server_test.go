// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package inspector

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestServerStartServesIndex spins up the server on an ephemeral port and
// asserts that /, /index.html, and an unknown SPA route all return 200
// with the embedded index.html bytes.
func TestServerStartServesIndex(t *testing.T) {
	port := pickFreePort(t)

	srv := New(Config{Port: port})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- srv.Start(ctx) }()

	waitForPort(t, port, 2*time.Second)

	for _, path := range []string{"/", "/index.html", "/some/spa/route"} {
		t.Run(path, func(t *testing.T) {
			resp, err := http.Get("http://127.0.0.1:" + itoa(port) + path)
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

func waitForPort(t *testing.T, port int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+itoa(port), 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("port %d did not open within %s", port, timeout)
}

func itoa(i int) string {
	// Local helper to avoid pulling strconv into the test file's mental model.
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	var buf [11]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = digits[i%10]
		i /= 10
	}
	return string(buf[pos:])
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n])
}
