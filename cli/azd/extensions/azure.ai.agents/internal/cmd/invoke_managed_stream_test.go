// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"io"
	"strings"
	"testing"
)

// TestStreamManagedSSE_TerminalEvents asserts a failed harness run is reported
// as an error. Returning nil here would make `azd ai agent invoke` exit 0 with
// no output, which is indistinguishable from an empty answer in CI.
func TestStreamManagedSSE_TerminalEvents(t *testing.T) {
	tests := []struct {
		name    string
		stream  string
		wantErr bool
		wantSub string
	}{
		{
			name: "error event",
			stream: "event: error\n" +
				`data: {"error":{"message":"model deployment not found","code":"NotFound"}}` + "\n\n",
			wantErr: true,
			wantSub: "model deployment not found",
		},
		{
			name: "response.failed",
			stream: "event: response.failed\n" +
				`data: {"response":{"id":"resp_1","error":{"message":"tool call failed"}}}` + "\n\n",
			wantErr: true,
			wantSub: "tool call failed",
		},
		{
			name: "response.incomplete",
			stream: "event: response.incomplete\n" +
				`data: {"response":{"id":"resp_2","incomplete_details":{"reason":"max_output_tokens"}}}` + "\n\n",
			wantErr: true,
			wantSub: "max_output_tokens",
		},
		{
			name: "terminal event with no details",
			stream: "event: error\n" +
				"data: \n\n",
			wantErr: true,
			wantSub: "produced no response",
		},
		{
			name: "successful run",
			stream: "event: response.created\n" +
				`data: {"response":{"id":"resp_3"}}` + "\n\n" +
				"event: response.output_text.delta\n" +
				`data: {"delta":"hello"}` + "\n\n" +
				"event: response.completed\n" +
				`data: {"response":{"id":"resp_3"}}` + "\n\n",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sb strings.Builder
			_, err := streamManagedSSE(strings.NewReader(tt.stream), &sb)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				if tt.wantSub != "" && !strings.Contains(err.Error(), tt.wantSub) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.wantSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestStreamManagedSSE_ReturnsResponseID confirms the response id is captured
// so the next invoke can chain via previous_response_id.
func TestStreamManagedSSE_ReturnsResponseID(t *testing.T) {
	stream := "event: response.completed\n" + `data: {"response":{"id":"resp_abc"}}` + "\n\n"
	id, err := streamManagedSSE(strings.NewReader(stream), io.Discard)
	if err != nil {
		t.Fatalf("streamManagedSSE: %v", err)
	}
	if id != "resp_abc" {
		t.Errorf("response id: got %q, want resp_abc", id)
	}
}
