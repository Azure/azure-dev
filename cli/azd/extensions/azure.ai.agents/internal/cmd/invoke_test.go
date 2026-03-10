// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
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
		result  map[string]interface{}
		wantErr bool
		errMsg  string
	}{
		{
			name: "successful output_text",
			result: map[string]interface{}{
				"status": "completed",
				"output": []interface{}{
					map[string]interface{}{
						"content": []interface{}{
							map[string]interface{}{
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
			result: map[string]interface{}{
				"status": "failed",
				"error": map[string]interface{}{
					"code":    "timeout",
					"message": "agent timed out",
				},
			},
			wantErr: true,
			errMsg:  "agent failed (timeout): agent timed out",
		},
		{
			name: "failed status without error details",
			result: map[string]interface{}{
				"status": "failed",
			},
			wantErr: true,
			errMsg:  "agent returned failed status",
		},
		{
			name: "server error code",
			result: map[string]interface{}{
				"code":    "server_error",
				"message": "internal error",
			},
			wantErr: true,
			errMsg:  "agent error (server_error): internal error",
		},
		{
			name: "no output key prints JSON",
			result: map[string]interface{}{
				"status": "completed",
				"id":     "resp_123",
			},
			wantErr: false,
		},
		{
			name: "empty output array prints JSON",
			result: map[string]interface{}{
				"output": []interface{}{},
			},
			wantErr: false,
		},
		{
			name: "content with non-output_text type is skipped",
			result: map[string]interface{}{
				"output": []interface{}{
					map[string]interface{}{
						"content": []interface{}{
							map[string]interface{}{
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
