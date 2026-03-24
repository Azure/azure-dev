// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"io"
	"net/http"
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
		name    string
		input   string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "simple data lines",
			input:   "data: Hello \ndata: world!\n\n",
			wantErr: false,
		},
		{
			name:    "DONE signal ends stream",
			input:   "data: Hello\ndata: [DONE]\ndata: ignored\n\n",
			wantErr: false,
		},
		{
			name:    "error envelope in data",
			input:   `data: {"error": {"code": "rate_limit", "message": "too many requests"}}` + "\n\n",
			wantErr: true,
			errMsg:  "agent error (rate_limit): too many requests",
		},
		{
			name:    "error envelope with type only",
			input:   `data: {"error": {"type": "server_error", "message": "crash"}}` + "\n\n",
			wantErr: true,
			errMsg:  "agent error (server_error): crash",
		},
		{
			name:    "empty stream",
			input:   "",
			wantErr: false,
		},
		{
			name:    "non-data lines ignored",
			input:   "event: custom\nid: 123\ndata: content\n\n",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reader := strings.NewReader(tt.input)
			err := handleInvocationSSE(reader, "test-agent")

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

			err := handleInvocationResponse(resp, "", "", "test-agent", 10*time.Second)

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
