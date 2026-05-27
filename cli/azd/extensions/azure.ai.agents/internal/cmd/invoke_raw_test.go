// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func TestWriteRawResponse(t *testing.T) {
	t.Parallel()

	t.Run("nil response returns error", func(t *testing.T) {
		t.Parallel()
		err := writeRawResponse(io.Discard, nil)
		if err == nil {
			t.Fatal("expected error for nil response")
		}
	})

	t.Run("headers are alphabetically sorted", func(t *testing.T) {
		t.Parallel()
		resp := &http.Response{
			Proto:      "HTTP/1.1",
			StatusCode: 200,
			Status:     "200 OK",
			Header:     http.Header{},
			Body:       http.NoBody,
		}
		// Add headers in non-alphabetical order.
		resp.Header.Set("X-Request-Id", "req-001")
		resp.Header.Set("Content-Type", "application/json")
		resp.Header.Set("X-Agent-Session-Id", "sess-1")
		resp.Header.Set("Cache-Control", "no-store")

		var buf bytes.Buffer
		if err := writeRawResponse(&buf, resp); err != nil {
			t.Fatalf("writeRawResponse: %v", err)
		}

		out := buf.String()
		// Determine the order in which header keys appear.
		want := []string{
			"Cache-Control:",
			"Content-Type:",
			"X-Agent-Session-Id:",
			"X-Request-Id:",
		}
		lastIdx := -1
		for _, h := range want {
			idx := strings.Index(out, h)
			if idx == -1 {
				t.Fatalf("header %q missing from output:\n%s", h, out)
			}
			if idx <= lastIdx {
				t.Errorf("header %q appeared out of alphabetical order in output:\n%s", h, out)
			}
			lastIdx = idx
		}
	})

	t.Run("status line and CRLF separator emitted", func(t *testing.T) {
		t.Parallel()
		resp := &http.Response{
			Proto:      "HTTP/2.0",
			StatusCode: 404,
			Status:     "404 Not Found",
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("nope")),
		}
		resp.Header.Set("Content-Type", "text/plain")

		var buf bytes.Buffer
		if err := writeRawResponse(&buf, resp); err != nil {
			t.Fatalf("writeRawResponse: %v", err)
		}
		out := buf.String()

		if !strings.HasPrefix(out, "HTTP/2.0 404 Not Found\r\n") {
			t.Errorf("missing status line, got: %q", out)
		}
		if !strings.Contains(out, "Content-Type: text/plain\r\n\r\nnope") {
			t.Errorf("missing CRLF separator before body, got:\n%s", out)
		}
	})

	t.Run("multi-valued headers preserved", func(t *testing.T) {
		t.Parallel()
		resp := &http.Response{
			Proto:      "HTTP/1.1",
			StatusCode: 200,
			Status:     "200 OK",
			Header:     http.Header{},
			Body:       http.NoBody,
		}
		resp.Header.Add("Set-Cookie", "a=1")
		resp.Header.Add("Set-Cookie", "b=2")

		var buf bytes.Buffer
		if err := writeRawResponse(&buf, resp); err != nil {
			t.Fatalf("writeRawResponse: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "Set-Cookie: a=1\r\n") ||
			!strings.Contains(out, "Set-Cookie: b=2\r\n") {
			t.Errorf("multi-valued Set-Cookie not preserved, got:\n%s", out)
		}
	})

	t.Run("nil body produces no body bytes", func(t *testing.T) {
		t.Parallel()
		resp := &http.Response{
			Proto:      "HTTP/1.1",
			StatusCode: 204,
			Status:     "204 No Content",
			Header:     http.Header{},
			Body:       nil,
		}

		var buf bytes.Buffer
		if err := writeRawResponse(&buf, resp); err != nil {
			t.Fatalf("writeRawResponse: %v", err)
		}
		out := buf.String()
		if !strings.HasSuffix(out, "\r\n\r\n") {
			t.Errorf("expected output to end with blank line and no body, got: %q", out)
		}
	})

	t.Run("empty status formatted from StatusCode", func(t *testing.T) {
		t.Parallel()
		resp := &http.Response{
			StatusCode: 418,
			Header:     http.Header{},
			Body:       http.NoBody,
		}
		var buf bytes.Buffer
		if err := writeRawResponse(&buf, resp); err != nil {
			t.Fatalf("writeRawResponse: %v", err)
		}
		out := buf.String()
		if !strings.HasPrefix(out, "HTTP/1.1 418 I'm a teapot\r\n") {
			t.Errorf("expected status line synthesized from StatusCode, got: %q", out)
		}
	})
}

// TestCaptureResponseSession_SilentInRawMode verifies that the empty-label
// contract used by --output raw still persists the server-assigned session
// (so subsequent invokes can reuse it) but writes nothing to stdout.
func TestCaptureResponseSession_SilentInRawMode(t *testing.T) {
	configSrv := newInvokeUserConfigServer()
	azdClient := newInvokeTestAzdClient(t, configSrv)

	const agentKey = "localhost:8088/proj/agents/test/versions/latest/local"
	const newSid = "sess-server-assigned"

	resp := &http.Response{
		Header: http.Header{},
		Body:   http.NoBody,
	}
	resp.Header.Set("x-agent-session-id", newSid)

	// Empty label => raw mode contract: no stdout output.
	stdout, err := captureStdout(t, func() error {
		captureResponseSession(t.Context(), azdClient, agentKey, "", resp, "")
		return nil
	})
	if err != nil {
		t.Fatalf("captureStdout: %v", err)
	}
	if stdout != "" {
		t.Errorf("expected silent stdout in raw mode, got: %q", stdout)
	}

	// Session must still have been persisted to the sessions store. The whole
	// "sessions" map is JSON-encoded at the "extensions.ai-agents.sessions"
	// path, so we check that the session ID appears in any value at any
	// recorded path.
	configSrv.mu.Lock()
	defer configSrv.mu.Unlock()
	persisted := false
	for path, val := range configSrv.values {
		if strings.Contains(path, ".sessions") && bytes.Contains(val, []byte(newSid)) {
			persisted = true
			break
		}
	}
	if !persisted {
		t.Errorf(
			"expected session %q to be persisted under *.sessions path, configSrv.values = %v",
			newSid, configSrv.values,
		)
	}
}

// TestCaptureResponseSession_PrintsWhenLabelProvided is the default-mode
// counterpart: a non-empty label means the user sees the session line.
func TestCaptureResponseSession_PrintsWhenLabelProvided(t *testing.T) {
	configSrv := newInvokeUserConfigServer()
	azdClient := newInvokeTestAzdClient(t, configSrv)

	resp := &http.Response{Header: http.Header{}, Body: http.NoBody}
	resp.Header.Set("x-agent-session-id", "sess-loud")

	stdout, err := captureStdout(t, func() error {
		captureResponseSession(
			t.Context(), azdClient, "k", "", resp, "Session: ",
		)
		return nil
	})
	if err != nil {
		t.Fatalf("captureStdout: %v", err)
	}
	if !strings.Contains(stdout, "Session: sess-loud") {
		t.Errorf("expected session line printed, got: %q", stdout)
	}
}

// TestHandleInvocationResponse_RawDumpsErrorBody asserts that --output raw
// dumps a 4xx response (status + headers + body) to stdout AND returns a
// concise error without re-embedding the body.
func TestHandleInvocationResponse_RawDumpsErrorBody(t *testing.T) {
	resp := &http.Response{
		StatusCode: 400,
		Status:     "400 Bad Request",
		Proto:      "HTTP/1.1",
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"error":"bad"}`)),
		Request:    &http.Request{},
	}
	resp.Header.Set("Content-Type", "application/json")
	resp.Header.Set("X-Trace-Id", "trace-001")

	stdout, err := captureStdout(t, func() error {
		return handleInvocationResponse(
			t.Context(), resp, "", "", "test-agent", 10*time.Second, nil, true,
		)
	})

	if err == nil {
		t.Fatal("expected error for 4xx in raw mode")
	}
	// Concise error: must NOT include the body (already on stdout).
	if strings.Contains(err.Error(), `"error":"bad"`) {
		t.Errorf("error should not duplicate body in raw mode, got: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "HTTP 400") {
		t.Errorf("error should reference HTTP 400, got: %q", err.Error())
	}
	// Stdout must contain the full raw dump.
	if !strings.Contains(stdout, "HTTP/1.1 400 Bad Request") {
		t.Errorf("stdout missing status line, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Content-Type: application/json") {
		t.Errorf("stdout missing Content-Type header, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "X-Trace-Id: trace-001") {
		t.Errorf("stdout missing X-Trace-Id header, got:\n%s", stdout)
	}
	if !strings.HasSuffix(stdout, `{"error":"bad"}`) {
		t.Errorf("stdout missing body verbatim, got:\n%s", stdout)
	}
}

// TestHandleInvocationLRO_Raw asserts the initial 202 + separator + final
// terminal response shape on stdout for a successful long-running invocation.
func TestHandleInvocationLRO_Raw(t *testing.T) {
	origInterval := defaultLROPollInterval
	defaultLROPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { defaultLROPollInterval = origInterval })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Final-Header", "final")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"status":"completed","result":"done"}`))
	}))
	defer srv.Close()

	reqURL, _ := url.Parse(srv.URL + "/invocations?api-version=test")
	resp := &http.Response{
		StatusCode: http.StatusAccepted,
		Status:     "202 Accepted",
		Proto:      "HTTP/1.1",
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"status":"accepted"}`)),
		Request:    &http.Request{URL: reqURL},
	}
	resp.Header.Set("x-agent-invocation-id", "inv-raw-1")
	resp.Header.Set("X-Initial-Header", "initial")

	stdout, err := captureStdout(t, func() error {
		return handleInvocationLRO(
			t.Context(), resp, "", "", "test-agent", 10*time.Second, nil, true,
		)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify ordering: initial dump comes before separator comes before final dump.
	initialIdx := strings.Index(stdout, "X-Initial-Header: initial")
	sepIdx := strings.Index(stdout, "\r\n---\r\n")
	finalIdx := strings.Index(stdout, "X-Final-Header: final")
	if initialIdx == -1 || sepIdx == -1 || finalIdx == -1 {
		t.Fatalf("missing expected markers in stdout:\n%s", stdout)
	}
	if !(initialIdx < sepIdx && sepIdx < finalIdx) {
		t.Errorf("unexpected ordering initial=%d sep=%d final=%d, stdout:\n%s",
			initialIdx, sepIdx, finalIdx, stdout)
	}

	// Initial 202 status line and body must be present before the separator.
	if !strings.Contains(stdout[:sepIdx], "202 Accepted") {
		t.Errorf("initial dump missing 202 status:\n%s", stdout[:sepIdx])
	}
	if !strings.Contains(stdout[:sepIdx], `{"status":"accepted"}`) {
		t.Errorf("initial dump missing 202 body:\n%s", stdout[:sepIdx])
	}

	// Final dump should contain the completed body.
	if !strings.Contains(stdout[finalIdx:], `"status":"completed"`) {
		t.Errorf("final dump missing completed body:\n%s", stdout[finalIdx:])
	}
}

// TestHandleInvocationLRO_RawPollErrorDoesNotDuplicateBody asserts that when a
// poll returns 5xx in raw mode, the body is dumped to stdout once and the
// returned error stays concise (no second copy of the body).
func TestHandleInvocationLRO_RawPollErrorDoesNotDuplicateBody(t *testing.T) {
	origInterval := defaultLROPollInterval
	defaultLROPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { defaultLROPollInterval = origInterval })

	const errBody = `{"error":"internal","details":"db down"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(errBody))
	}))
	defer srv.Close()

	reqURL, _ := url.Parse(srv.URL + "/invocations?api-version=test")
	resp := &http.Response{
		StatusCode: http.StatusAccepted,
		Status:     "202 Accepted",
		Proto:      "HTTP/1.1",
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"status":"accepted"}`)),
		Request:    &http.Request{URL: reqURL},
	}
	resp.Header.Set("x-agent-invocation-id", "inv-err-1")

	stdout, err := captureStdout(t, func() error {
		return handleInvocationLRO(
			t.Context(), resp, "", "", "test-agent", 10*time.Second, nil, true,
		)
	})

	if err == nil {
		t.Fatal("expected poll error")
	}
	if strings.Contains(err.Error(), errBody) {
		t.Errorf("poll error in raw mode should not duplicate body, got: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("poll error missing HTTP 500: %q", err.Error())
	}
	if strings.Count(stdout, errBody) != 1 {
		t.Errorf("expected body exactly once in stdout, got count=%d in:\n%s",
			strings.Count(stdout, errBody), stdout)
	}
}

// TestInvokeOutputFlagValidation verifies the extension SDK rejects unknown
// --output values up-front with a clear "supported: ..." error, before any
// HTTP traffic. The invoke command no longer registers its own --output flag
// (it would conflict with azd's reserved global --output); it instead opts
// into per-command allowed values via azdext.RegisterFlagOptions, and this
// test exercises that wiring through a real SDK root command.
func TestInvokeOutputFlagValidation(t *testing.T) {
	// No t.Parallel — azdext.NewExtensionRootCommand sets the package-level
	// cobra.EnableTraverseRunHooks=true, and several tests cooperate to share
	// process-global cobra state.

	tests := []struct {
		name string
		args []string
	}{
		{name: "rejects --output foo", args: []string{"invoke", "--output", "foo", "hi"}},
		{name: "rejects --output json (not yet supported)", args: []string{"invoke", "--output", "json", "hi"}},
		{name: "rejects -o yaml", args: []string{"invoke", "-o", "yaml", "hi"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{Name: "agent"})
			rootCmd.SilenceUsage = true
			rootCmd.SilenceErrors = true
			rootCmd.AddCommand(newInvokeCommand(extCtx))
			rootCmd.SetArgs(tt.args)
			rootCmd.SetOut(io.Discard)
			rootCmd.SetErr(io.Discard)

			err := rootCmd.Execute()
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), "--output") {
				t.Errorf("error should mention --output, got: %q", err.Error())
			}
			if !strings.Contains(err.Error(), "supported:") {
				t.Errorf("error should list supported values, got: %q", err.Error())
			}
		})
	}
}

// TestInvokeCommandRegistersRawOutputOption asserts the invoke command attaches
// the per-command allowed-values annotation used by the SDK to validate and
// auto-complete the inherited --output flag. This guards against accidental
// removal of the RegisterFlagOptions call, which would silently re-break the
// `--output raw` UX without any test failure on the existing raw-mode tests.
func TestInvokeCommandRegistersRawOutputOption(t *testing.T) {
	t.Parallel()

	cmd := newInvokeCommand(nil)
	encoded, ok := cmd.Annotations["azdext.allowed-values/output"]
	if !ok {
		t.Fatal("invoke command is missing the azdext.allowed-values/output annotation")
	}

	var values []string
	if err := json.Unmarshal([]byte(encoded), &values); err != nil {
		t.Fatalf("decode allowed values annotation: %v", err)
	}

	for _, want := range []string{outputDefault, outputRaw} {
		if !slices.Contains(values, want) {
			t.Errorf("expected %q in allowed values, got %v", want, values)
		}
	}
}
