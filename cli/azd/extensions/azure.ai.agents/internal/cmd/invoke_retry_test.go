// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ---- doLocalRequestWithRetry + isConnectionRefused (issue #8411) ----

func TestIsConnectionRefused(t *testing.T) {
	t.Parallel()
	// Dial a port we just released so the connection is actively refused.
	ln, port := listenLoopback(t)
	_ = ln.Close()
	_, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err == nil {
		t.Skip("dial to a released port unexpectedly succeeded")
	}
	if !isConnectionRefused(err) {
		t.Fatalf("expected isConnectionRefused=true for %v", err)
	}
	if isConnectionRefused(context.DeadlineExceeded) {
		t.Fatalf("expected isConnectionRefused=false for a non-refused error")
	}
}

func TestDoLocalRequestWithRetry_SucceedsImmediately(t *testing.T) {
	t.Parallel()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	newReq := func() (*http.Request, error) {
		return http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL, strings.NewReader("hi"))
	}
	resp, err := doLocalRequestWithRetry(t.Context(), srv.Client(), newReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("expected exactly 1 request, got %d", n)
	}
}

// TestDoLocalRequestWithRetry_ReturnsHTTPErrorsWithoutRetry proves the retry
// only covers transport-level refusals: a reachable server that answers with a
// non-2xx status is returned immediately (one call), not retried.
func TestDoLocalRequestWithRetry_ReturnsHTTPErrorsWithoutRetry(t *testing.T) {
	t.Parallel()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	newReq := func() (*http.Request, error) {
		return http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL, strings.NewReader("hi"))
	}
	resp, err := doLocalRequestWithRetry(t.Context(), srv.Client(), newReq)
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("expected exactly 1 request (no retry on HTTP errors), got %d", n)
	}
}

// TestDoLocalRequestWithRetry_RetriesUntilListenerReady simulates the issue
// #8411 race: the first connect is refused because the agent's listener hasn't
// bound yet, and a later retry succeeds once it comes up.
func TestDoLocalRequestWithRetry_RetriesUntilListenerReady(t *testing.T) {
	t.Parallel()
	ln, port := listenLoopback(t)
	_ = ln.Close() // port now refuses connections

	srvCh := make(chan *http.Server, 1)
	t.Cleanup(func() {
		select {
		case srv := <-srvCh:
			_ = srv.Close()
		default:
		}
	})

	go func() {
		// Delay binding so at least the first connect attempt is refused.
		time.Sleep(250 * time.Millisecond)
		var l net.Listener
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			var err error
			l, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
			if err == nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if l == nil {
			return
		}
		srv := &http.Server{
			Handler:           http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }),
			ReadHeaderTimeout: time.Second,
		}
		srvCh <- srv
		_ = srv.Serve(l)
	}()

	reqURL := fmt.Sprintf("http://127.0.0.1:%d/", port)
	newReq := func() (*http.Request, error) {
		return http.NewRequestWithContext(t.Context(), http.MethodPost, reqURL, strings.NewReader("hi"))
	}
	resp, err := doLocalRequestWithRetry(t.Context(), &http.Client{}, newReq)
	if err != nil {
		t.Fatalf("expected success after retry, got %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

// TestDoLocalRequestWithRetry_HonorsContextCancellation ensures a never-binding
// port doesn't hang for the full retry budget: cancelling ctx exits promptly.
func TestDoLocalRequestWithRetry_HonorsContextCancellation(t *testing.T) {
	t.Parallel()
	ln, port := listenLoopback(t)
	_ = ln.Close() // stays refused for the whole test

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	reqURL := fmt.Sprintf("http://127.0.0.1:%d/", port)
	newReq := func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader("hi"))
	}
	start := time.Now()
	if _, err := doLocalRequestWithRetry(ctx, &http.Client{}, newReq); err == nil {
		t.Fatalf("expected an error when the port never binds")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("retry did not exit promptly on ctx cancel (took %s)", elapsed)
	}
}
