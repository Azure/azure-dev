// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/project"

	"github.com/stretchr/testify/require"
)

func TestManagedResponsesRequestAgentReference(t *testing.T) {
	for _, harness := range []string{"", "github_copilot_preview"} {
		for _, version := range []string{"", "7"} {
			t.Run("harness="+harness+"/version="+version, func(t *testing.T) {
				pctx := &promptServiceContext{
					ServiceName: "service-name",
					Agent: agent_yaml.PromptAgent{
						AgentDefinition: agent_yaml.AgentDefinition{Name: "deployed-name"},
						Model:           "model",
						Harness:         agent_yaml.NewPromptHarness(harness),
					},
				}
				action := &InvokeAction{flags: &invokeFlags{version: version}}
				request := action.buildPromptResponsesRequest(pctx, "hello", "resp_previous")
				payload, err := json.Marshal(request)
				require.NoError(t, err)
				var body map[string]any
				require.NoError(t, json.Unmarshal(payload, &body))
				require.Equal(t, "hello", body["input"])
				require.Equal(t, "resp_previous", body["previous_response_id"])
				require.Equal(t, true, body["stream"])
				require.NotContains(t, body, "agent_session_id")
				if harness != "" && version == "" {
					require.NotContains(t, body, "agent_reference")
					return
				}
				want := map[string]any{"type": "agent_reference", "name": "deployed-name"}
				if version != "" {
					want["version"] = version
				}
				require.Equal(t, want, body["agent_reference"])
			})
		}
	}
}

func TestPromptInvokeVersionState(t *testing.T) {
	userConfig := newInvokeUserConfigServer()
	azdClient := newInvokeTestAzdClient(t, userConfig)
	pctx := &promptServiceContext{
		Settings: &project.PromptAgentSettings{ProjectEndpoint: "https://test.services.ai.azure.com/api/projects/proj"},
	}
	latestKey := pctx.agentKey("agent", "")
	versionKey := pctx.agentKey("agent", "7")
	require.NotEqual(t, latestKey, versionKey)
	userConfig.setJSON(t, configPath("conversations"), map[string]string{
		latestKey:  "resp_latest",
		versionKey: "resp_v7",
	})
	for _, tt := range []struct {
		name  string
		flags invokeFlags
		want  string
	}{
		{name: "default", want: "resp_latest"},
		{name: "pinned", flags: invokeFlags{version: "7"}, want: "resp_v7"},
		{name: "different version", flags: invokeFlags{version: "8"}},
		{name: "reset conversation", flags: invokeFlags{version: "7", newConversation: true}},
		{name: "reset session", flags: invokeFlags{version: "7", newSession: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			action := &InvokeAction{flags: &tt.flags}
			key := pctx.agentKey("agent", tt.flags.version)
			require.Equal(t, tt.want, action.managedPreviousResponseID(t.Context(), azdClient, key))
		})
	}
	saveContextValue(t.Context(), azdClient, versionKey, "resp_v7_next", "conversations")
	var stored map[string]string
	userConfig.getJSON(t, configPath("conversations"), &stored)
	require.Equal(t, map[string]string{latestKey: "resp_latest", versionKey: "resp_v7_next"}, stored)
}

func TestManagedResponsesRequestOmitsPreviousResponseForNewSession(t *testing.T) {
	userConfig := newInvokeUserConfigServer()
	azdClient := newInvokeTestAzdClient(t, userConfig)
	agentKey := "managed-agent-key"
	userConfig.setJSON(t, configPath("conversations"), map[string]string{agentKey: "resp_previous"})
	action := &InvokeAction{flags: &invokeFlags{newSession: true}}

	payload, err := json.Marshal(managedResponsesRequest{
		Model:              "model",
		Input:              "hello",
		PreviousResponseID: action.managedPreviousResponseID(t.Context(), azdClient, agentKey),
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if strings.Contains(string(payload), `"previous_response_id"`) {
		t.Errorf("new session request must omit previous_response_id: %s", payload)
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
	if _, err := streamManagedSSE(strings.NewReader(sse), &out); err != nil {
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
	if _, err := streamManagedSSE(strings.NewReader(sse), &out); err != nil {
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
	if _, err := streamManagedSSE(strings.NewReader(sse), &out); err != nil {
		t.Fatalf("streamManagedSSE: %v", err)
	}
	if out.String() != "ok\n" {
		t.Errorf("got %q, want %q", out.String(), "ok\n")
	}
}

// TestStreamManagedSSE_CapturesResponseID asserts the response id is parsed
// from lifecycle events so the caller can chain the next turn via
// previous_response_id. The last id seen (from response.completed) wins.
func TestStreamManagedSSE_CapturesResponseID(t *testing.T) {
	sse := strings.Join([]string{
		"event: response.created",
		`data: {"type":"response.created","response":{"id":"resp_created"}}`,
		"",
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta","delta":"hi"}`,
		"",
		"event: response.completed",
		`data: {"type":"response.completed","response":{"id":"resp_done"}}`,
		"",
	}, "\n")

	var out strings.Builder
	id, err := streamManagedSSE(strings.NewReader(sse), &out)
	if err != nil {
		t.Fatalf("streamManagedSSE: %v", err)
	}
	if id != "resp_done" {
		t.Errorf("got response id %q, want %q", id, "resp_done")
	}
	if out.String() != "hi\n" {
		t.Errorf("got %q, want %q", out.String(), "hi\n")
	}
}
