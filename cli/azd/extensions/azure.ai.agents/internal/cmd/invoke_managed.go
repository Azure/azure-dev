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
// specific managed agent. It mirrors the shape the managed harness expects
// (see test-e2e-foundry-tools.sh): `agent_reference: {type, name}`.
type managedAgentReference struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

// managedResponsesRequest is the OpenAI-shape Responses request body sent to
// the workspace-rooted /openai/responses endpoint for a managed agent.
type managedResponsesRequest struct {
	Model          string                 `json:"model"`
	Input          string                 `json:"input"`
	Stream         bool                   `json:"stream"`
	AgentReference *managedAgentReference `json:"agent_reference,omitempty"`
	Tools          []any                  `json:"tools"`
	Conversation   *managedConversation   `json:"conversation,omitempty"`
}

type managedConversation struct {
	ID string `json:"id"`
}

func (a *InvokeAction) storedManagedConversationID(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	agentKey string,
) string {
	if explicit := strings.TrimSpace(a.flags.conversation); explicit != "" {
		if azdClient != nil {
			saveContextValue(ctx, azdClient, agentKey, explicit, "conversations")
		}
		return explicit
	}
	if azdClient == nil || a.flags.forceNewConversation() {
		return ""
	}
	value, err := getContextValueWithFallback(ctx, azdClient, "conversations", agentKey, nil)
	if err != nil || !strings.HasPrefix(value, "conv_") {
		return ""
	}
	return value
}

func promptConversationEndpoint(projectEndpoint, agentName string, harnessed bool) string {
	projectEndpoint = strings.TrimRight(projectEndpoint, "/")
	if !harnessed {
		return projectEndpoint + "/openai/v1/conversations"
	}
	return fmt.Sprintf(
		"%s/agents/%s/endpoint/protocols/openai/conversations?api-version=v1",
		projectEndpoint, agentName,
	)
}

// runPromptInvoke sends a message to a prompt (kind=managed) agent via the
// harness Responses API and streams the assistant's reply to stdout.
//
// The target harness and agent identity come from the resolved azure.yaml
// service (promptServiceContext), so prompt agents invoke through the same
// service resolution as hosted agents.
func (a *InvokeAction) runPromptInvoke(ctx context.Context, pctx *promptServiceContext) error {
	if timeout := a.httpTimeout(); timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	agentName := pctx.AgentName()
	if strings.TrimSpace(agentName) == "" {
		return exterrors.Validation(
			exterrors.CodeInvalidAgentName,
			"agent name could not be resolved",
			"set 'name' on the agent service in azure.yaml or pass the agent name as the first argument",
		)
	}

	body, _, err := a.resolveBody()
	if err != nil {
		return err
	}

	// Persist a platform conversation per agent so later turns retain context.
	agentKey := pctx.agentKey(agentName)
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		log.Printf("invoke prompt: config store unavailable, multi-turn memory disabled: %v", err)
		azdClient = nil
	}
	if azdClient != nil {
		defer azdClient.Close()
	}

	request := managedResponsesRequest{
		Model:  pctx.Agent.Model,
		Input:  string(body),
		Stream: true,
		Tools:  []any{},
	}
	if pctx.Agent.HarnessType() == "" {
		request.AgentReference = &managedAgentReference{Type: "agent_reference", Name: agentName}
	}
	client, err := pctx.newClient(ctx)
	if err != nil {
		return err
	}

	headers := map[string]string{
		// The harness forwards model calls to this gateway. Required by the
		// V3 harness engine (see test-e2e-foundry-tools.sh).
		"x-model-endpoint": pctx.Settings.EffectiveModelEndpoint(),
	}
	conversationID := a.storedManagedConversationID(ctx, azdClient, agentKey)
	if conversationID == "" {
		harnessed := pctx.Agent.HarnessType() != ""
		conversationEndpoint := promptConversationEndpoint(pctx.Settings.ProjectEndpoint, agentName, harnessed)
		if harnessed {
			headers["Foundry-Features"] = "GitHubCopilot=V1Preview"
		}
		conversationID, err = client.CreateConversationAt(ctx, conversationEndpoint, headers)
		if err != nil {
			return exterrors.ServiceFromAzure(err, exterrors.OpCreateConversation)
		}
		if azdClient != nil {
			saveContextValue(ctx, azdClient, agentKey, conversationID, "conversations")
		}
	}
	request.Conversation = &managedConversation{ID: conversationID}
	payload, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("building prompt invoke request: %w", err)
	}

	var stream io.ReadCloser
	if pctx.Agent.HarnessType() == "" {
		stream, _, err = client.CreateResponseStream(ctx, payload, headers)
	} else {
		endpoint := fmt.Sprintf(
			"%s/agents/%s/endpoint/protocols/openai/responses?api-version=v1",
			strings.TrimRight(pctx.Settings.ProjectEndpoint, "/"), agentName,
		)
		headers["Foundry-Features"] = "GitHubCopilot=V1Preview"
		stream, _, err = client.CreateResponseStreamAt(ctx, endpoint, payload, headers)
	}
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpCreateAgent)
	}
	defer stream.Close()

	if err := streamManagedSSE(stream, os.Stdout); err != nil {
		return fmt.Errorf("reading prompt agent response stream: %w", err)
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
// Terminal failure events (`error`, `response.failed`, `response.incomplete`)
// return an error. Reporting success with no output would make a failed
// invocation indistinguishable from an empty answer and exit 0 in CI.
func streamManagedSSE(r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	// SSE data lines can be large (full JSON payloads); raise the buffer cap
	// well above the 64 KiB default so a single event never overflows it.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var event string
	var streamErr error
	completed := false
	wroteText := false
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			switch {
			case event == "response.output_text.delta":
				var payload struct {
					Delta string `json:"delta"`
				}
				if err := json.Unmarshal([]byte(data), &payload); err == nil && payload.Delta != "" {
					fmt.Fprint(w, payload.Delta)
					wroteText = true
				}
			case event == "error" || event == "response.failed" || event == "response.incomplete":
				if streamErr == nil {
					streamErr = managedStreamFailure(event, data)
				}
			case strings.HasPrefix(event, "response."):
				if event == "response.completed" {
					completed = true
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
	if err := scanner.Err(); err != nil {
		return err
	}
	if streamErr == nil && !completed {
		return fmt.Errorf("managed response stream ended before response.completed")
	}
	return streamErr
}

// managedStreamFailure builds an error from a terminal SSE event, preferring
// the service-supplied message over the raw payload.
func managedStreamFailure(event, data string) error {
	var payload struct {
		Message string `json:"message"`
		Error   struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
		Response struct {
			IncompleteDetails struct {
				Reason string `json:"reason"`
			} `json:"incomplete_details"`
			Error struct {
				Message string `json:"message"`
				Code    string `json:"code"`
			} `json:"error"`
		} `json:"response"`
	}
	_ = json.Unmarshal([]byte(data), &payload)

	for _, candidate := range []string{
		payload.Error.Message,
		payload.Response.Error.Message,
		payload.Message,
		payload.Response.IncompleteDetails.Reason,
	} {
		if strings.TrimSpace(candidate) != "" {
			return fmt.Errorf("%s: %s", event, candidate)
		}
	}
	if strings.TrimSpace(data) != "" {
		return fmt.Errorf("%s: %s", event, data)
	}
	return fmt.Errorf("the agent run ended with %q and produced no response", event)
}
