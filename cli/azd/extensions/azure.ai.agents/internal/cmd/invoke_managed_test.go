// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestManagedResponsesRequestAgentReference(t *testing.T) {
	plain, err := json.Marshal(managedResponsesRequest{
		Model: "model",
		AgentReference: &managedAgentReference{
			Type: "agent_reference",
			Name: "plain",
		},
	})
	if err != nil {
		t.Fatalf("marshal plain request: %v", err)
	}
	if !strings.Contains(string(plain), `"agent_reference"`) {
		t.Errorf("plain request must carry agent_reference: %s", plain)
	}

	harnessed, err := json.Marshal(managedResponsesRequest{Model: "model"})
	if err != nil {
		t.Fatalf("marshal harnessed request: %v", err)
	}
	if strings.Contains(string(harnessed), `"agent_reference"`) {
		t.Errorf("harnessed request must omit agent_reference: %s", harnessed)
	}
	withConversation, err := json.Marshal(managedResponsesRequest{
		Model:        "model",
		Conversation: &managedConversation{ID: "conv_123"},
	})
	if err != nil {
		t.Fatalf("marshal conversation request: %v", err)
	}
	if !strings.Contains(string(withConversation), `"conversation":{"id":"conv_123"}`) {
		t.Errorf("request must carry conversation id: %s", withConversation)
	}
	if strings.Contains(string(withConversation), `"previous_response_id"`) {
		t.Errorf("request must not carry previous_response_id: %s", withConversation)
	}
}

func TestManagedConversationState(t *testing.T) {
	userConfig := newInvokeUserConfigServer()
	azdClient := newInvokeTestAzdClient(t, userConfig)
	agentKey := "managed-agent-key"
	userConfig.setJSON(t, configPath("conversations"), map[string]string{agentKey: "conv_previous"})
	action := &InvokeAction{flags: &invokeFlags{}}
	if got := action.storedManagedConversationID(t.Context(), azdClient, agentKey); got != "conv_previous" {
		t.Fatalf("conversation id: got %q", got)
	}
	action.flags.newConversation = true
	if got := action.storedManagedConversationID(t.Context(), azdClient, agentKey); got != "" {
		t.Fatalf("new conversation should not reuse %q", got)
	}
	action.flags.conversation = " conv_explicit "
	if got := action.storedManagedConversationID(t.Context(), azdClient, agentKey); got != "conv_explicit" {
		t.Fatalf("explicit conversation id: got %q", got)
	}
	var conversations map[string]string
	userConfig.getJSON(t, configPath("conversations"), &conversations)
	if got := conversations[agentKey]; got != "conv_explicit" {
		t.Fatalf("stored explicit conversation id: got %q", got)
	}
}

func TestPromptConversationEndpoint(t *testing.T) {
	tests := []struct {
		name      string
		harnessed bool
		want      string
	}{
		{
			name: "prompt agent",
			want: "https://test.example.com/api/projects/proj/openai/v1/conversations",
		},
		{
			name:      "harness agent",
			harnessed: true,
			want: "https://test.example.com/api/projects/proj/agents/agent/endpoint/protocols/openai/" +
				"conversations?api-version=v1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := promptConversationEndpoint(
				"https://test.example.com/api/projects/proj/", "agent", tt.harnessed,
			)
			if got != tt.want {
				t.Errorf("endpoint: got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestStreamManagedSSE_TextDeltas asserts only output_text.delta events are
// rendered, in order, with a trailing newline, and that lifecycle events are
// consumed silently.
func TestStreamManagedSSE_TextDeltas(t *testing.T) {
	sse := strings.Join([]string{
		"event: response.created",
		`data: {"type":"response.created"}`,
		"",
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta","delta":"Hello"}`,
		"",
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta","delta":", world"}`,
		"",
		"event: response.completed",
		`data: {"type":"response.completed"}`,
		"",
	}, "\n")

	var out strings.Builder
	if err := streamManagedSSE(strings.NewReader(sse), &out); err != nil {
		t.Fatalf("streamManagedSSE: %v", err)
	}
	got := out.String()
	want := "Hello, world\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestStreamManagedSSE_NoText asserts that a stream with no text deltas
// produces no output (and notably no trailing newline).
func TestStreamManagedSSE_NoText(t *testing.T) {
	sse := strings.Join([]string{
		"event: response.created",
		`data: {"type":"response.created"}`,
		"",
		"event: response.completed",
		`data: {"type":"response.completed"}`,
		"",
	}, "\n")

	var out strings.Builder
	if err := streamManagedSSE(strings.NewReader(sse), &out); err != nil {
		t.Fatalf("streamManagedSSE: %v", err)
	}
	if out.String() != "" {
		t.Errorf("expected empty output, got %q", out.String())
	}
}

// TestStreamManagedSSE_IgnoresMalformedData asserts a malformed data line does
// not abort the stream or emit garbage.
func TestStreamManagedSSE_IgnoresMalformedData(t *testing.T) {
	sse := strings.Join([]string{
		"event: response.output_text.delta",
		`data: {not valid json`,
		"",
		"event: response.output_text.delta",
		`data: {"delta":"ok"}`,
		"",
		"event: response.completed",
		`data: {"response":{"id":"resp_ok"}}`,
		"",
	}, "\n")

	var out strings.Builder
	if err := streamManagedSSE(strings.NewReader(sse), &out); err != nil {
		t.Fatalf("streamManagedSSE: %v", err)
	}
	if out.String() != "ok\n" {
		t.Errorf("got %q, want %q", out.String(), "ok\n")
	}
}
