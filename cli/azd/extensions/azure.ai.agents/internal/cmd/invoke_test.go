// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadSSEStream(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr bool
		errMsg  string
	}{
		{
			name: "text deltas followed by completed",
			input: "event: response.output_text.delta\n" +
				`data: {"delta":"Hello "}` + "\n\n" +
				"event: response.output_text.delta\n" +
				`data: {"delta":"world!"}` + "\n\n" +
				"event: response.completed\n" +
				`data: {"response":{"status":"completed","output":[]}}` + "\n\n",
			wantErr: false,
		},
		{
			name: "completed with no deltas and output_text in response",
			input: "event: response.completed\n" +
				`data: {"response":{"status":"completed","output":[{"content":[{"type":"output_text","text":"Hi there"}]}]}}` + "\n\n",
			wantErr: false,
		},
		{
			name: "failed status in completed event",
			input: "event: response.completed\n" +
				`data: {"response":{"status":"failed","error":{"code":"runtime_error","message":"agent crashed"}}}` + "\n\n",
			wantErr: true,
			errMsg:  "agent failed (runtime_error): agent crashed",
		},
		{
			name: "failed status without error details",
			input: "event: response.completed\n" +
				`data: {"response":{"status":"failed"}}` + "\n\n",
			wantErr: true,
			errMsg:  "agent returned failed status",
		},
		{
			name: "error event with structured error",
			input: "event: error\n" +
				`data: {"code":"rate_limit","message":"too many requests"}` + "\n\n",
			wantErr: true,
			errMsg:  "agent error (rate_limit): too many requests",
		},
		{
			name: "error event with unstructured data",
			input: "event: error\n" +
				"data: something went wrong\n\n",
			wantErr: true,
			errMsg:  "agent stream error: something went wrong",
		},
		{
			name:    "empty stream",
			input:   "",
			wantErr: false,
		},
		{
			name:    "blank lines only",
			input:   "\n\n\n",
			wantErr: false,
		},
		{
			name: "unknown event types are ignored",
			input: "event: response.created\n" +
				`data: {"id":"resp_123"}` + "\n\n" +
				"event: response.completed\n" +
				`data: {"response":{"status":"completed","output":[]}}` + "\n\n",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reader := strings.NewReader(tt.input)
			err := readSSEStream(reader, "test-agent")

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errMsg != "" && err.Error() != tt.errMsg {
					t.Errorf("error = %q, want %q", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestPrintAgentResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		result  map[string]any
		wantErr bool
		errMsg  string
	}{
		{
			name: "successful output_text",
			result: map[string]any{
				"status": "completed",
				"output": []any{
					map[string]any{
						"content": []any{
							map[string]any{
								"type": "output_text",
								"text": "Hello from agent",
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "failed status with error details",
			result: map[string]any{
				"status": "failed",
				"error": map[string]any{
					"code":    "timeout",
					"message": "agent timed out",
				},
			},
			wantErr: true,
			errMsg:  "agent failed (timeout): agent timed out",
		},
		{
			name: "failed status without error details",
			result: map[string]any{
				"status": "failed",
			},
			wantErr: true,
			errMsg:  "agent returned failed status",
		},
		{
			name: "server error code",
			result: map[string]any{
				"code":    "server_error",
				"message": "internal error",
			},
			wantErr: true,
			errMsg:  "agent error (server_error): internal error",
		},
		{
			name: "no output key prints JSON",
			result: map[string]any{
				"status": "completed",
				"id":     "resp_123",
			},
			wantErr: false,
		},
		{
			name: "empty output array prints JSON",
			result: map[string]any{
				"output": []any{},
			},
			wantErr: false,
		},
		{
			name: "content with non-output_text type is skipped",
			result: map[string]any{
				"output": []any{
					map[string]any{
						"content": []any{
							map[string]any{
								"type": "image",
								"url":  "https://example.com/img.png",
							},
						},
					},
				},
			},
			wantErr: false, // Falls through to JSON print
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := printAgentResponse(tt.result, "test")

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errMsg != "" && err.Error() != tt.errMsg {
					t.Errorf("error = %q, want %q", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestHttpTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		timeout int
		want    time.Duration
	}{
		{name: "positive value", timeout: 120, want: 120 * time.Second},
		{name: "zero means no timeout", timeout: 0, want: 0},
		{name: "negative means no timeout", timeout: -1, want: 0},
		{name: "custom value", timeout: 300, want: 300 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			action := &InvokeAction{
				flags: &invokeFlags{timeout: tt.timeout},
			}
			got := action.httpTimeout()
			if got != tt.want {
				t.Errorf("httpTimeout() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHandleInvocationSync(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "json result",
			body:    `{"result": "hello"}`,
			wantErr: false,
		},
		{
			name:    "plain text result",
			body:    "Hello from agent",
			wantErr: false,
		},
		{
			name:    "error envelope with code",
			body:    `{"error": {"code": "bad_request", "message": "invalid input"}}`,
			wantErr: true,
			errMsg:  "agent error (bad_request): invalid input",
		},
		{
			name:    "error envelope with type",
			body:    `{"error": {"type": "validation_error", "message": "missing field"}}`,
			wantErr: true,
			errMsg:  "agent error (validation_error): missing field",
		},
		{
			name:    "error envelope with no code or type",
			body:    `{"error": {"message": "something went wrong"}}`,
			wantErr: true,
			errMsg:  "agent error: something went wrong",
		},
		{
			name:    "empty body",
			body:    "",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reader := strings.NewReader(tt.body)
			err := handleInvocationSync(reader, "test-agent")

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errMsg != "" && err.Error() != tt.errMsg {
					t.Errorf("error = %q, want %q", err.Error(), tt.errMsg)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestHandleInvocationSSE(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		input      string
		agentName  string
		wantErr    bool
		errMsg     string
		wantOutput string
	}{
		{
			name:       "simple data lines produce separate output lines with prefix",
			input:      "data: Hello \ndata: world!\n\n",
			agentName:  "test-agent",
			wantOutput: "[test-agent] Hello \nworld!\n",
		},
		{
			name:       "single data line gets prefix and newline",
			input:      "data: only-one\n\n",
			agentName:  "my-bot",
			wantOutput: "[my-bot] only-one\n",
		},
		{
			name:       "DONE signal ends stream, only preceding data printed",
			input:      "data: Hello\ndata: [DONE]\ndata: ignored\n\n",
			agentName:  "test-agent",
			wantOutput: "[test-agent] Hello\n",
		},
		{
			name:      "error envelope in data",
			input:     `data: {"error": {"code": "rate_limit", "message": "too many requests"}}` + "\n\n",
			agentName: "test-agent",
			wantErr:   true,
			errMsg:    "agent error (rate_limit): too many requests",
		},
		{
			name:      "error envelope with type only",
			input:     `data: {"error": {"type": "server_error", "message": "crash"}}` + "\n\n",
			agentName: "test-agent",
			wantErr:   true,
			errMsg:    "agent error (server_error): crash",
		},
		{
			name:       "empty stream produces no output",
			input:      "",
			agentName:  "test-agent",
			wantOutput: "",
		},
		{
			name:       "non-data lines ignored",
			input:      "event: custom\nid: 123\ndata: content\n\n",
			agentName:  "test-agent",
			wantOutput: "[test-agent] content\n",
		},
		{
			name:       "three data lines produce three output lines",
			input:      "data: line1\ndata: line2\ndata: line3\n\n",
			agentName:  "agent",
			wantOutput: "[agent] line1\nline2\nline3\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			reader := strings.NewReader(tt.input)
			err := handleInvocationSSE(&buf, reader, tt.agentName)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errMsg != "" && err.Error() != tt.errMsg {
					t.Errorf("error = %q, want %q", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got := buf.String(); got != tt.wantOutput {
					t.Errorf("output mismatch\ngot:  %q\nwant: %q", got, tt.wantOutput)
				}
			}
		})
	}
}

func TestHandleInvocationResponse_Routing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		statusCode  int
		contentType string
		body        string
		headers     map[string]string
		wantErr     bool
		errContains string
	}{
		{
			name:        "200 with JSON routes to sync",
			statusCode:  200,
			contentType: "application/json",
			body:        `{"result": "ok"}`,
			wantErr:     false,
		},
		{
			name:        "200 with SSE routes to streaming",
			statusCode:  200,
			contentType: "text/event-stream",
			body:        "data: hello\n\n",
			wantErr:     false,
		},
		{
			name:        "202 without invocation ID returns error",
			statusCode:  202,
			contentType: "application/json",
			body:        `{"status": "accepted"}`,
			wantErr:     true,
			errContains: "no invocation ID found",
		},
		{
			name:        "400 returns HTTP error",
			statusCode:  400,
			contentType: "application/json",
			body:        `{"error": "bad request"}`,
			wantErr:     true,
			errContains: "HTTP 400",
		},
		{
			name:        "500 returns HTTP error",
			statusCode:  500,
			contentType: "text/plain",
			body:        "Internal Server Error",
			wantErr:     true,
			errContains: "HTTP 500",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp := &http.Response{
				StatusCode: tt.statusCode,
				Status:     http.StatusText(tt.statusCode),
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader(tt.body)),
				Request:    &http.Request{},
			}
			resp.Header.Set("Content-Type", tt.contentType)
			for k, v := range tt.headers {
				resp.Header.Set(k, v)
			}

			err := handleInvocationResponse(t.Context(), resp, "", "", "test-agent", 10*time.Second)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error = %q, want containing %q", err.Error(), tt.errContains)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestResolveBody(t *testing.T) {
	t.Parallel()

	t.Run("message string", func(t *testing.T) {
		t.Parallel()

		action := &InvokeAction{
			flags: &invokeFlags{message: "Hello!"},
		}
		body, label, err := action.resolveBody()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(body) != "Hello!" {
			t.Errorf("body = %q, want %q", string(body), "Hello!")
		}
		if !strings.Contains(label, "Hello!") {
			t.Errorf("label = %q, want it to contain the message", label)
		}
	})

	t.Run("input file", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "request.json")
		content := `{"task": "summarize", "text": "long text..."}`
		if err := os.WriteFile(filePath, []byte(content), 0600); err != nil {
			t.Fatalf("failed to write temp file: %v", err)
		}

		action := &InvokeAction{
			flags: &invokeFlags{inputFile: filePath},
		}
		body, label, err := action.resolveBody()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(body) != content {
			t.Errorf("body = %q, want %q", string(body), content)
		}
		if !strings.Contains(label, "request.json") {
			t.Errorf("label = %q, want it to contain the filename", label)
		}
	})

	t.Run("missing file returns error", func(t *testing.T) {
		t.Parallel()

		action := &InvokeAction{
			flags: &invokeFlags{inputFile: "/nonexistent/path/file.json"},
		}
		_, _, err := action.resolveBody()
		if err == nil {
			t.Fatal("expected error for missing file, got nil")
		}
		if !strings.Contains(err.Error(), "failed to read input file") {
			t.Errorf("error = %q, want it to mention failed to read", err.Error())
		}
	})
}

func TestHandleInvocationLRO(t *testing.T) {
	// Override poll interval for fast tests. Not parallel since we modify a package var.
	origInterval := defaultLROPollInterval
	defaultLROPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { defaultLROPollInterval = origInterval })

	tests := []struct {
		name string
		// initial202Header sets the x-agent-invocation-id on the 202 response.
		// If empty, initial202Body should contain invocation_id.
		initial202Header string
		initial202Body   string
		// pollResponses is a sequence of responses the poll server returns.
		// Each entry is a status code + body pair.
		pollResponses []pollStep
		timeout       time.Duration
		wantErr       bool
		errContains   string
	}{
		{
			name:             "happy path — completed on first poll",
			initial202Header: "inv-001",
			initial202Body:   `{"status":"accepted"}`,
			pollResponses: []pollStep{
				{status: 200, body: `{"status":"completed","result":"done"}`},
			},
			timeout: 10 * time.Second,
			wantErr: false,
		},
		{
			name:             "invocation ID from body when header is missing",
			initial202Header: "",
			initial202Body:   `{"invocation_id":"inv-from-body","status":"accepted"}`,
			pollResponses: []pollStep{
				{status: 200, body: `{"status":"completed","result":"ok"}`},
			},
			timeout: 10 * time.Second,
			wantErr: false,
		},
		{
			name:             "poll returns running then completed",
			initial202Header: "inv-002",
			initial202Body:   `{"status":"accepted"}`,
			pollResponses: []pollStep{
				{status: 200, body: `{"status":"running"}`},
				{status: 200, body: `{"status":"completed","result":"done"}`},
			},
			timeout: 10 * time.Second,
			wantErr: false,
		},
		{
			name:             "poll returns 404 then completed",
			initial202Header: "inv-003",
			initial202Body:   `{}`,
			pollResponses: []pollStep{
				{status: 404, body: "not found"},
				{status: 200, body: `{"status":"completed","result":"ok"}`},
			},
			timeout: 10 * time.Second,
			wantErr: false,
		},
		{
			name:             "poll returns failed with error details",
			initial202Header: "inv-004",
			initial202Body:   `{}`,
			pollResponses: []pollStep{
				{status: 200, body: `{"status":"failed","error":{"code":"runtime_error","message":"agent crashed"}}`},
			},
			timeout:     10 * time.Second,
			wantErr:     true,
			errContains: "invocation failed (runtime_error): agent crashed",
		},
		{
			name:             "poll returns cancelled",
			initial202Header: "inv-005",
			initial202Body:   `{}`,
			pollResponses: []pollStep{
				{status: 200, body: `{"status":"cancelled"}`},
			},
			timeout:     10 * time.Second,
			wantErr:     true,
			errContains: "invocation was cancelled",
		},
		{
			name:             "poll returns HTTP 500 error",
			initial202Header: "inv-006",
			initial202Body:   `{}`,
			pollResponses: []pollStep{
				{status: 500, body: "Internal Server Error"},
			},
			timeout:     10 * time.Second,
			wantErr:     true,
			errContains: "HTTP 500",
		},
		{
			name:             "timeout waiting for completion",
			initial202Header: "inv-007",
			initial202Body:   `{}`,
			pollResponses: []pollStep{
				// Always returns running — will repeat until timeout
				{status: 200, body: `{"status":"running"}`, repeat: true},
			},
			timeout:     100 * time.Millisecond,
			wantErr:     true,
			errContains: "timed out",
		},
		{
			name:             "retry-after header is respected",
			initial202Header: "inv-008",
			initial202Body:   `{}`,
			pollResponses: []pollStep{
				{status: 200, body: `{"status":"running"}`, retryAfter: "1"},
				{status: 200, body: `{"status":"completed","result":"ok"}`},
			},
			timeout: 10 * time.Second,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pollIndex := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if pollIndex >= len(tt.pollResponses) {
					// Repeat last response if marked as repeating
					last := tt.pollResponses[len(tt.pollResponses)-1]
					if last.repeat {
						if last.retryAfter != "" {
							w.Header().Set("Retry-After", last.retryAfter)
						}
						w.WriteHeader(last.status)
						_, _ = w.Write([]byte(last.body))
						return
					}
					// Shouldn't reach here in well-formed tests
					w.WriteHeader(500)
					_, _ = w.Write([]byte("unexpected poll"))
					return
				}
				step := tt.pollResponses[pollIndex]
				pollIndex++
				if step.retryAfter != "" {
					w.Header().Set("Retry-After", step.retryAfter)
				}
				w.WriteHeader(step.status)
				_, _ = w.Write([]byte(step.body))
			}))
			defer srv.Close()

			// Build a fake 202 response whose Request.URL points at our test server
			// so the poll URL derivation works.
			reqURL, _ := url.Parse(srv.URL + "/invocations?api-version=test")
			resp := &http.Response{
				StatusCode: http.StatusAccepted,
				Status:     "202 Accepted",
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader(tt.initial202Body)),
				Request:    &http.Request{URL: reqURL},
			}
			if tt.initial202Header != "" {
				resp.Header.Set("x-agent-invocation-id", tt.initial202Header)
			}

			err := handleInvocationLRO(t.Context(), resp, "", "", "test-agent", tt.timeout)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error = %q, want containing %q", err.Error(), tt.errContains)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

type pollStep struct {
	status     int
	body       string
	retryAfter string
	repeat     bool
}
