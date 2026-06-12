// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// All tests in this file are sequential (not parallel) because the integration
// tests mutate the global os.Stdout via withCapturedStdout.

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0.000s"},
		{1 * time.Millisecond, "0.001s"},
		{943 * time.Millisecond, "0.943s"},
		{1000 * time.Millisecond, "1.000s"},
		{6667 * time.Millisecond, "6.667s"},
		{14727 * time.Millisecond, "14.727s"},
	}

	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			if got := formatDuration(tc.d); got != tc.want {
				t.Errorf("formatDuration(%v) = %q, want %q", tc.d, got, tc.want)
			}
		})
	}
}

func TestPrintInvokeTiming(t *testing.T) {
	var buf bytes.Buffer
	printInvokeTiming(&buf, 19734*time.Millisecond, 13697*time.Millisecond)
	got := buf.String()

	for _, want := range []string{"⏱", "19.734s", "first byte: 13.697s"} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q missing %q", got, want)
		}
	}
}

func TestResponsesLocal_Timing(t *testing.T) {
	okBody, _ := json.Marshal(map[string]any{
		"output": []any{map[string]any{"content": []any{map[string]any{"type": "output_text", "text": "hi"}}}},
	})

	cases := []struct {
		name      string
		status    int
		body      string
		raw       bool
		wantTimer bool
	}{
		{"success", 200, string(okBody), false, true},
		{"failure", 500, "error", false, false},
		{"raw_mode", 200, string(okBody), true, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()

			outputFmt := ""
			if tc.raw {
				outputFmt = outputRaw
			}

			action := &InvokeAction{
				flags:    &invokeFlags{message: "hi", port: testPort(t, srv.URL), local: true, protocol: "responses", outputFmt: outputFmt},
				noPrompt: true,
			}

			output := withCapturedStdout(t, func() { _ = action.responsesLocal(t.Context()) })

			if tc.wantTimer && !strings.Contains(output, "⏱") {
				t.Errorf("expected timing, got:\n%s", output)
			}
			if !tc.wantTimer && strings.Contains(output, "⏱") {
				t.Errorf("unexpected timing in output:\n%s", output)
			}
		})
	}
}

func TestInvocationsLocal_Timing(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		status      int
		body        string
		raw         bool
		wantTimer   bool
	}{
		{"sync_json_success", "application/json", 200, `{"result":"ok"}`, false, true},
		{"sse_success", "text/event-stream", 200, "data: hello\n\n", false, true},
		{"failure", "application/json", 400, `{"error":"bad"}`, false, false},
		{"raw_mode", "application/json", 200, `{"result":"ok"}`, true, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/openapi") {
					w.WriteHeader(404)
					return
				}
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()

			outputFmt := ""
			if tc.raw {
				outputFmt = outputRaw
			}

			action := &InvokeAction{
				flags:    &invokeFlags{message: "hi", port: testPort(t, srv.URL), local: true, protocol: "invocations", outputFmt: outputFmt},
				noPrompt: true,
			}

			output := withCapturedStdout(t, func() { _ = action.invocationsLocal(t.Context()) })

			if tc.wantTimer && !strings.Contains(output, "⏱") {
				t.Errorf("expected timing, got:\n%s", output)
			}
			if !tc.wantTimer && strings.Contains(output, "⏱") {
				t.Errorf("unexpected timing in output:\n%s", output)
			}
		})
	}
}

// --- helpers ---

// withCapturedStdout redirects os.Stdout to a pipe, runs fn, then returns
// everything written to stdout. Uses t.Cleanup to guarantee restoration even
// if the test fails or panics.
func withCapturedStdout(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}

	orig := os.Stdout
	os.Stdout = w

	t.Cleanup(func() {
		os.Stdout = orig
		_ = w.Close()
		_ = r.Close()
	})

	fn()

	_ = w.Close()
	os.Stdout = orig

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("io.ReadAll: %v", err)
	}
	_ = r.Close()

	return string(out)
}

func testPort(t *testing.T, rawURL string) int {
	t.Helper()
	parts := strings.Split(rawURL, ":")
	var port int
	if _, err := fmt.Sscanf(parts[len(parts)-1], "%d", &port); err != nil {
		t.Fatalf("cannot parse port from %q: %v", rawURL, err)
	}
	return port
}
