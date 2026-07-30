// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// managedAgentReference is the body fragment that binds a Responses call to a
// specific managed agent. It mirrors the shape the vienna harness expects
// (see test-e2e-foundry-tools.sh): `agent_reference: {type, name}`.
type managedAgentReference struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

// managedResponsesRequest is the OpenAI-shape Responses request body sent to
// the workspace-rooted /openai/responses endpoint for a managed agent.
type managedResponsesRequest struct {
	Model          string                `json:"model"`
	Input          string                `json:"input"`
	Stream         bool                  `json:"stream"`
	AgentReference managedAgentReference `json:"agent_reference"`
	Tools          []any                 `json:"tools"`
	// PreviousResponseID chains this turn to the previous one so the harness
	// restores prior conversation context (multi-turn memory). Empty on the
	// first turn of a conversation; omitted from the payload when empty.
	PreviousResponseID string `json:"previous_response_id,omitempty"`
}

// runPromptInvoke sends a message to a prompt (kind=managed) agent via the
// harness Responses API and streams the assistant's reply to stdout.
//
// The target harness and agent identity come from the resolved azure.yaml
// service (promptServiceContext), so prompt agents invoke through the same
// service resolution as hosted agents.
func (a *InvokeAction) runPromptInvoke(ctx context.Context, pctx *promptServiceContext) error {
	agentName := a.flags.name
	if agentName == "" {
		agentName = pctx.AgentName()
	}
	if strings.TrimSpace(agentName) == "" {
		return exterrors.Validation(
			exterrors.CodeInvalidAgentName,
			"agent name could not be resolved",
			"set 'name' in agent.yaml or pass the agent name as the first argument",
		)
	}

	body, _, err := a.resolveBody()
	if err != nil {
		return err
	}

	// Resolve multi-turn state. Prompt agents chain turns via the OpenAI
	// Responses `previous_response_id`: azd persists the last response id per
	// agent and sends it on the next invoke so the harness restores prior
	// conversation context. Best-effort — a config-store failure degrades to a
	// stateless (single-turn) invoke rather than blocking the call.
	agentKey := pctx.agentKey(agentName)
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		log.Printf("invoke prompt: config store unavailable, multi-turn memory disabled: %v", err)
		azdClient = nil
	}
	if azdClient != nil {
		defer azdClient.Close()
	}

	var previousResponseID string
	if azdClient != nil && !a.flags.newConversation {
		if val, gerr := getContextValueWithFallback(ctx, azdClient, "conversations", agentKey, nil); gerr == nil {
			previousResponseID = val
		}
	}

	payload, err := json.Marshal(managedResponsesRequest{
		Model:              pctx.Agent.Model,
		Input:              string(body),
		Stream:             true,
		AgentReference:     managedAgentReference{Type: "agent_reference", Name: agentName},
		Tools:              []any{},
		PreviousResponseID: previousResponseID,
	})
	if err != nil {
		return fmt.Errorf("building prompt invoke request: %w", err)
	}

	client, err := pctx.newClient()
	if err != nil {
		return err
	}

	headers := map[string]string{
		// The harness forwards model calls to this gateway. Required by the
		// V3 harness engine (see test-e2e-foundry-tools.sh).
		"x-model-endpoint": pctx.Settings.EffectiveModelEndpoint(),
	}

	stream, _, err := client.CreateResponseStream(ctx, payload, headers)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpCreateAgent)
	}
	defer stream.Close()

	responseID, err := streamManagedSSE(stream, os.Stdout)
	if err != nil {
		return fmt.Errorf("reading prompt agent response stream: %w", err)
	}

	// Persist the new response id so the next invoke continues this thread.
	if azdClient != nil && responseID != "" {
		saveContextValue(ctx, azdClient, agentKey, responseID, "conversations")
	}
	return nil
}

// streamManagedSSE scans a Server-Sent Events stream from the harness Responses
// API and writes the assistant's text to w as it arrives.
//
// Only `response.output_text.delta` events produce visible output; lifecycle
// events (`response.created`, `response.completed`, etc.) are consumed
// silently. A trailing newline is emitted after the stream ends so the shell
// prompt returns on its own line.
//
// The returned string is the response id parsed from the stream's lifecycle
// events (when present), which the caller persists so the next invoke can
// chain via `previous_response_id` for multi-turn memory.
func streamManagedSSE(r io.Reader, w io.Writer) (string, error) {
	scanner := bufio.NewScanner(r)
	// SSE data lines can be large (full JSON payloads); raise the buffer cap
	// well above the 64 KiB default so a single event never overflows it.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var event string
	var responseID string
	wroteText := false
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if event == "response.output_text.delta" {
				var payload struct {
					Delta string `json:"delta"`
				}
				if err := json.Unmarshal([]byte(data), &payload); err == nil && payload.Delta != "" {
					fmt.Fprint(w, payload.Delta)
					wroteText = true
				}
			} else if strings.HasPrefix(event, "response.") {
				// Capture the response id from any lifecycle event that carries
				// it (e.g. response.created, response.completed). The last one
				// seen wins so the persisted id reflects the completed turn.
				var payload struct {
					Response struct {
						ID string `json:"id"`
					} `json:"response"`
				}
				if err := json.Unmarshal([]byte(data), &payload); err == nil && payload.Response.ID != "" {
					responseID = payload.Response.ID
				}
			}
		case line == "":
			// Blank line terminates an SSE event block.
			event = ""
		}
	}
	if wroteText {
		fmt.Fprintln(w)
	}
	return responseID, scanner.Err()
}
