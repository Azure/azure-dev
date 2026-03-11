// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type invokeFlags struct {
	message         string
	local           bool
	name            string
	port            int
	timeout         int
	session         string
	newSession      bool
	conversation    string
	newConversation bool
}

type InvokeAction struct {
	flags *invokeFlags
}

func newInvokeCommand() *cobra.Command {
	flags := &invokeFlags{}

	cmd := &cobra.Command{
		Use:   "invoke [name] [message]",
		Short: "Send a message to your agent.",
		Long: `Send a message to your agent.

By default the agent is invoked remotely on Foundry. When a single
argument is provided it is treated as the message and the agent name
is auto-detected from azure.yaml. With two arguments the first is the
agent name and the second is the message.

Use --local to target a locally running agent (started via 'azd ai agent run')
instead of Foundry.

Sessions are persisted per-agent — consecutive invokes reuse the same
session automatically. Pass --new-session to force a reset.`,
		Example: `  # Invoke the remote agent on Foundry (auto-detects agent from azure.yaml)
  azd ai agent invoke "Hello!"

  # Invoke a specific remote agent by name
  azd ai agent invoke my-agent "Hello!"

  # Invoke locally (agent must be running via 'azd ai agent run')
  azd ai agent invoke --local "Hello!"

  # Start a new session (discard conversation history)
  azd ai agent invoke --new-session "Hello!"`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			setupDebugLogging(cmd.Flags())

			if len(args) == 2 {
				flags.name = args[0]
				flags.message = args[1]
			} else {
				flags.message = args[0]
			}

			if flags.name != "" && flags.local {
				return fmt.Errorf("cannot use --local with a named agent; named agents are always invoked remotely on Foundry")
			}

			action := &InvokeAction{flags: flags}
			return action.Run(ctx)
		},
	}

	cmd.Flags().BoolVarP(&flags.local, "local", "l", false, "Invoke on localhost instead of Foundry")
	cmd.Flags().IntVar(&flags.port, "port", DefaultPort, "Local server port")
	cmd.Flags().IntVarP(&flags.timeout, "timeout", "t", 120, "Request timeout in seconds (0 for no timeout)")
	cmd.Flags().StringVarP(&flags.session, "session", "s", "", "Explicit session ID override")
	cmd.Flags().BoolVar(&flags.newSession, "new-session", false, "Force a new session (discard saved one)")
	cmd.Flags().StringVar(&flags.conversation, "conversation", "", "Explicit conversation ID override")
	cmd.Flags().BoolVar(&flags.newConversation, "new-conversation", false, "Force a new conversation (discard saved one)")

	return cmd
}

func (a *InvokeAction) Run(ctx context.Context) error {
	if a.flags.local {
		return a.invokeLocal(ctx)
	}
	return a.invokeRemote(ctx)
}

func (a *InvokeAction) httpTimeout() time.Duration {
	if a.flags.timeout <= 0 {
		return 0 // no timeout
	}
	return time.Duration(a.flags.timeout) * time.Second
}

func (a *InvokeAction) invokeLocal(ctx context.Context) error {
	port := a.flags.port
	msg := a.flags.message

	fmt.Printf("Target:  localhost:%d (local)\n", port)
	fmt.Printf("Message: %q\n\n", msg)

	body := map[string]any{
		"input": msg,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("http://localhost:%d/responses", port)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: a.httpTimeout()}
	resp, err := client.Do(req) //nolint:gosec // G704: URL targets localhost with user-configured port
	if err != nil {
		return fmt.Errorf("could not connect to localhost:%d — is the agent running? Start it with: azd ai agent run", port)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		requestID := resp.Header.Get("apim-request-id")
		if requestID != "" {
			fmt.Printf("Trace ID: %s\n", requestID)
		}
		return fmt.Errorf("HTTP %d: %s\n%s", resp.StatusCode, resp.Status, string(respBody))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		// Not JSON — just print raw response
		fmt.Println(string(respBody))
		return nil
	}

	return printAgentResponse(result, "local")
}

func (a *InvokeAction) invokeRemote(ctx context.Context) error {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return fmt.Errorf("failed to create azd client: %w", err)
	}
	defer azdClient.Close()

	name := a.flags.name

	// Auto-resolve agent name from azure.yaml
	if info, err := resolveAgentServiceFromProject(ctx, azdClient, name, rootFlags.NoPrompt); err == nil {
		if name == "" && info.AgentName != "" {
			name = info.AgentName
		}
	}

	if name == "" {
		return fmt.Errorf("agent name is required; provide as the first argument or define an azure.ai.agent service in azure.yaml")
	}

	endpoint, err := resolveAgentEndpoint(ctx, "", "")
	if err != nil {
		return err
	}

	msg := a.flags.message

	// Build request body — uses streaming to receive the full agent response.
	body := map[string]any{
		"input": msg,
		"agent": map[string]string{
			"name": name,
			"type": "agent_reference",
		},
		"stream": true,
	}

	// Session ID — routes to the same microVM container instance
	sid, err := resolveSessionID(ctx, azdClient, name, a.flags.session, a.flags.newSession)
	if err != nil {
		return err
	}
	body["session_id"] = sid

	// Acquire credential and token — used for both conversation creation and the invoke request
	credential, err := newAgentCredential()
	if err != nil {
		return err
	}

	token, err := credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://ai.azure.com/.default"},
	})
	if err != nil {
		return fmt.Errorf("failed to get auth token: %w", err)
	}

	// Conversation ID — enables multi-turn memory via Foundry Conversations API
	convID, err := resolveConversationID(
		ctx,
		azdClient,
		name,
		a.flags.conversation,
		a.flags.newConversation,
		endpoint,
		token.Token,
	)
	if err != nil {
		return err
	}
	body["conversation"] = map[string]string{"id": convID}

	fmt.Printf("Agent:        %s (remote)\n", name)
	fmt.Printf("Message:      %q\n", msg)
	fmt.Printf("Session:      %s\n", sid)
	fmt.Printf("Conversation: %s\n", convID)
	fmt.Println()

	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/openai/responses?api-version=%s", endpoint, DefaultAgentAPIVersion)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token.Token)

	client := &http.Client{Timeout: a.httpTimeout()}
	resp, err := client.Do(req) //nolint:gosec // G704: endpoint is resolved from azd environment configuration
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	requestID := resp.Header.Get("apim-request-id")
	if requestID != "" {
		fmt.Printf("Trace ID: %s\n", requestID)
	}

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s\n%s", resp.StatusCode, resp.Status, string(respBody))
	}

	// Parse SSE stream for agent output
	return readSSEStream(resp.Body, name)
}

// createConversation creates a new Foundry conversation for multi-turn memory.
func createConversation(ctx context.Context, endpoint string, bearerToken string) (string, error) {
	url := fmt.Sprintf("%s/openai/conversations?api-version=%s", endpoint, DefaultAgentAPIVersion)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader([]byte("{}")))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearerToken)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req) //nolint:gosec // G704: endpoint is resolved from azd environment configuration
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("failed to create conversation: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}

	if id, ok := result["id"].(string); ok {
		return id, nil
	}
	return "", fmt.Errorf("conversation response missing 'id' field")
}

// readSSEStream reads a Server-Sent Events stream from the Foundry Responses API,
// printing text deltas in real-time and returning the final response or any error.
func readSSEStream(body io.Reader, agentName string) error {
	scanner := bufio.NewScanner(body)
	// Allow large SSE data lines (up to 1 MB)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var currentEvent string
	var printed bool

	for scanner.Scan() {
		line := scanner.Text()

		if after, ok := strings.CutPrefix(line, "event: "); ok {
			currentEvent = after
			continue
		}

		if after, ok := strings.CutPrefix(line, "data: "); ok {
			data := after

			switch currentEvent {
			case "response.output_text.delta":
				var delta struct {
					Delta string `json:"delta"`
				}
				if err := json.Unmarshal([]byte(data), &delta); err == nil && delta.Delta != "" {
					if !printed {
						fmt.Printf("[%s] ", agentName)
						printed = true
					}
					fmt.Print(delta.Delta)
				}

			case "response.completed":
				if printed {
					fmt.Println()
				}
				// Parse the completed response to check for errors
				var event struct {
					Response json.RawMessage `json:"response"`
				}
				if err := json.Unmarshal([]byte(data), &event); err == nil && event.Response != nil {
					var result map[string]any
					if err := json.Unmarshal(event.Response, &result); err == nil {
						if status, _ := result["status"].(string); status == "failed" {
							if errObj, ok := result["error"].(map[string]any); ok {
								msg, _ := errObj["message"].(string)
								code, _ := errObj["code"].(string)
								return fmt.Errorf("agent failed (%s): %s", code, msg)
							}
							return fmt.Errorf("agent returned failed status")
						}
						// If no text was streamed, extract output from the completed response
						if !printed {
							return printAgentResponse(result, agentName)
						}
					}
				}
				return nil

			case "error":
				if printed {
					fmt.Println()
				}
				var sseErr struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				}
				if err := json.Unmarshal([]byte(data), &sseErr); err == nil {
					return fmt.Errorf("agent error (%s): %s", sseErr.Code, sseErr.Message)
				}
				return fmt.Errorf("agent stream error: %s", data)
			}

			currentEvent = ""
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading response stream: %w", err)
	}

	if printed {
		fmt.Println()
	}
	return nil
}

// printAgentResponse pretty-prints the output_text items from an agent response.
func printAgentResponse(result map[string]any, title string) error {
	// Check for agent-level errors (e.g., agent runtime failures)
	if status, _ := result["status"].(string); status == "failed" {
		if errObj, ok := result["error"].(map[string]any); ok {
			msg, _ := errObj["message"].(string)
			code, _ := errObj["code"].(string)
			return fmt.Errorf("agent failed (%s): %s", code, msg)
		}
		return fmt.Errorf("agent returned failed status")
	}

	// Check for server-level errors (e.g., local agentserver: {"code": "server_error", "message": "..."})
	if code, ok := result["code"].(string); ok && code != "" {
		msg, _ := result["message"].(string)
		return fmt.Errorf("agent error (%s): %s", code, msg)
	}

	outputItems, ok := result["output"].([]any)
	if !ok {
		// Try printing the whole response as formatted JSON
		jsonBytes, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(jsonBytes))
		return nil
	}

	printed := false
	for _, item := range outputItems {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		contentItems, ok := itemMap["content"].([]any)
		if !ok {
			continue
		}
		for _, content := range contentItems {
			contentMap, ok := content.(map[string]any)
			if !ok {
				continue
			}
			if contentMap["type"] == "output_text" {
				if text, ok := contentMap["text"].(string); ok {
					fmt.Printf("[%s] %s\n", title, text)
					printed = true
				}
			}
		}
	}

	if !printed {
		jsonBytes, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(jsonBytes))
	}
	return nil
}
