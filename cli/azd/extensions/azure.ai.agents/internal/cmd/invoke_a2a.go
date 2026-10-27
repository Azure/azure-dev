// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"azureaiagent/internal/cmd/nextstep"
	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/google/uuid"
)

// a2aVersionHeader selects the public A2A wire dialect, not the Foundry API or
// agent deployment version. Unsupported dialects produce VersionNotSupportedError.
// See https://a2a-protocol.org/v1.0.0/specification/#36-versioning.
const a2aVersionHeader = "A2A-Version"

// a2aJSONRPCVersion identifies the JSON-RPC envelope, not the A2A dialect.
const a2aJSONRPCVersion = "2.0"

// a2aJSONRPCRequest is the shared envelope around dialect-specific parameters.
type a2aJSONRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// a2aTextRequestBuilder constructs one dialect's complete plain-message request.
// IDs are supplied by the caller so builders are pure and independently testable.
type a2aTextRequestBuilder func(text, requestID, messageID string) a2aJSONRPCRequest

// a2aRequestBuilderForVersion is the single supported-dialect selection point for
// request construction and version validation. It never guesses from file input.
func a2aRequestBuilderForVersion(version string) (a2aTextRequestBuilder, error) {
	switch version {
	case "0.3":
		return buildA2A03TextRequest, nil
	case "1.0":
		return buildA2A10TextRequest, nil
	default:
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("invalid --a2a-version %q: supported versions are 0.3 and 1.0", version),
			"use --a2a-version 0.3 or --a2a-version 1.0",
		)
	}
}

// validateInvokeA2AVersionFlags validates explicit dialect selection. An empty
// protocol is unresolved, so route-specific checks must wait for resolution.
func validateInvokeA2AVersionFlags(flags *invokeFlags, protocol agent_api.AgentProtocol, preview bool) error {
	if flags.a2aVersion != "" {
		if err := validateA2AVersion(flags.a2aVersion); err != nil {
			return err
		}
		if protocol != "" && protocol != agent_api.AgentProtocolA2A {
			return exterrors.Validation(
				exterrors.CodeConflictingArguments,
				"--a2a-version is only supported with the a2a protocol",
				"omit --a2a-version or select --protocol a2a",
			)
		}
	}
	if preview && protocol == agent_api.AgentProtocolA2A && flags.inputFile != "" && flags.a2aVersion == "" {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--input-file with the a2a protocol requires an explicit --a2a-version",
			"pass --a2a-version 0.3 or --a2a-version 1.0 to match the file's request dialect",
		)
	}
	return nil
}

func validateA2AVersion(version string) error {
	_, err := a2aRequestBuilderForVersion(version)
	return err
}

// resolveA2AVersion fixes the wire version once at invocation entry.
func resolveA2AVersion(explicit string, preview bool) string {
	if explicit != "" {
		return explicit
	}
	if preview {
		return "1.0"
	}
	return "0.3"
}

// buildA2ARequestBody returns the JSON request body for an A2A invoke and a
// human-readable label describing it.
//
// When the user supplied --input-file, its bytes are forwarded verbatim — the
// file is assumed to already contain a complete JSON-RPC request (the A2A
// analogue of the invocations protocol's opaque body pass-through). Otherwise
// the plain message is wrapped in a JSON-RPC 2.0 request for the selected
// dialect, with a single user text part and that dialect's execution defaults.
// This method owns input handling and serialization; each public dialect's
// complete body is constructed independently by its selected builder.
func (a *InvokeAction) buildA2ARequestBody(version string) ([]byte, string, error) {
	buildRequest, err := a2aRequestBuilderForVersion(version)
	if err != nil {
		return nil, "", err
	}
	body, label, err := a.resolveBody()
	if err != nil {
		return nil, "", err
	}
	if a.flags.inputFile != "" {
		// User provided a full JSON-RPC request; forward it unchanged.
		return body, label, nil
	}

	request := buildRequest(string(body), uuid.NewString(), uuid.NewString())
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, "", fmt.Errorf("failed to marshal A2A request: %w", err)
	}
	return payload, label, nil
}

// a2a03MessageSendParams is the public A2A 0.3 message/send parameter shape.
// See https://a2a-protocol.org/v0.3.0/specification/#711-messagesendparams-object.
type a2a03MessageSendParams struct {
	Message a2a03Message `json:"message"`
}

type a2a03Message struct {
	Kind      string          `json:"kind"`
	MessageID string          `json:"messageId"`
	Role      string          `json:"role"`
	Parts     []a2a03TextPart `json:"parts"`
}

type a2a03TextPart struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// buildA2A03TextRequest constructs the complete 0.3 body with required message/part
// discriminators, preserving unspecified execution and output modes. File input
// never passes through this builder.
func buildA2A03TextRequest(text, requestID, messageID string) a2aJSONRPCRequest {
	return a2aJSONRPCRequest{
		JSONRPC: a2aJSONRPCVersion,
		ID:      requestID,
		Method:  "message/send",
		Params: a2a03MessageSendParams{
			Message: a2a03Message{
				Kind:      "message",
				MessageID: messageID,
				Role:      "user",
				Parts:     []a2a03TextPart{{Kind: "text", Text: text}},
			},
		},
	}
}

// a2a10SendMessageRequest is the public A2A 1.0 SendMessage parameter shape.
// See https://a2a-protocol.org/v1.0.0/specification/#9-json-rpc-protocol-binding.
type a2a10SendMessageRequest struct {
	Message       a2a10Message                  `json:"message"`
	Configuration a2a10MessageSendConfiguration `json:"configuration"`
}

type a2a10Message struct {
	MessageID string          `json:"messageId"`
	Role      string          `json:"role"`
	Parts     []a2a10TextPart `json:"parts"`
}

type a2a10TextPart struct {
	Text string `json:"text"`
}

type a2a10MessageSendConfiguration struct {
	AcceptedOutputModes []string `json:"acceptedOutputModes"`
	// False explicitly selects 1.0's default synchronous mode.
	ReturnImmediately bool `json:"returnImmediately"`
}

// buildA2A10TextRequest constructs the complete 1.0 body independently of 0.3.
// File input never passes through this builder.
func buildA2A10TextRequest(text, requestID, messageID string) a2aJSONRPCRequest {
	return a2aJSONRPCRequest{
		JSONRPC: a2aJSONRPCVersion,
		ID:      requestID,
		Method:  "SendMessage",
		Params: a2a10SendMessageRequest{
			Message: a2a10Message{
				MessageID: messageID,
				Role:      "ROLE_USER",
				Parts:     []a2a10TextPart{{Text: text}},
			},
			Configuration: a2a10MessageSendConfiguration{
				AcceptedOutputModes: []string{"text/plain"},
				ReturnImmediately:   false,
			},
		},
	}
}

// a2aLocal reports that this CLI does not yet support local A2A invocation.
// A2A applications can run locally, but azd currently targets Foundry only.
func (a *InvokeAction) a2aLocal(_ context.Context) error {
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		"the a2a protocol cannot be invoked against a local agent",
		"omit --local to invoke the deployed agent over a2a, "+
			"or use --protocol responses/invocations for local invocation",
	)
}

// applyA2ARequestHeaders sets the A2A-specific request headers: the explicit
// A2A-Version header (so the server negotiates deterministically instead of
// applying an implicit default), the Foundry bearer token, and the remote user
// identity header. The caller sets Content-Type and any raw-mode headers.
func applyA2ARequestHeaders(req *http.Request, bearerToken string, identity *userIdentityFlags, version string) {
	req.Header.Set(a2aVersionHeader, version)
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	applyRemoteUserIdentityHeader(req, identity)
}

// a2aRemote sends the user's message to a deployed Foundry agent using the A2A
// protocol (POST /agents/{name}/endpoint/protocols/a2a) with a JSON-RPC 2.0
// request in the selected dialect. Memory is bound to the agent session, like the
// invocations protocol.
func (a *InvokeAction) a2aRemote(ctx context.Context, version string) error {
	body, bodyLabel, err := a.buildA2ARequestBody(version)
	if err != nil {
		return err
	}

	rc, err := a.resolveRemoteContextForInvoke(ctx)
	if err != nil {
		return err
	}
	if rc.azdClient != nil {
		defer rc.azdClient.Close()
	}
	if err := a.validateA2ATarget(ctx, rc, version); err != nil {
		return err
	}
	a.reportInvokeUsageForRemote(ctx, agent_api.AgentProtocolA2A, rc)

	agentKey := rc.agentKey
	if agentKey == "" && rc.azdClient != nil {
		log.Printf("warning: agent endpoint not available, session state will not be persisted")
	}

	if a.flags.newConversation {
		fmt.Fprintln(os.Stderr,
			"note: --new-conversation has no effect for the a2a protocol "+
				"(memory is bound to the session; use --new-session to reset).")
	}

	// Acquire the bearer token after body validation so a local input error
	// (e.g., unreadable --input-file) does not pay an unnecessary auth round-trip
	// and is surfaced before any auth failure.
	rc.bearerToken, err = a.acquireBearerToken(ctx)
	if err != nil {
		return err
	}

	// Session ID — routes to the same container instance.
	sid, err := a.resolveRemoteSessionID(ctx, rc)
	if err != nil {
		return err
	}

	raw := a.flags.outputFmt == outputRaw
	if !raw {
		fmt.Printf("Agent:    %s (remote, a2a protocol)\n", rc.name)
		fmt.Printf("Input:    %s\n", bodyLabel)
		if rc.version != "" {
			fmt.Printf("Version:  %s\n", rc.version)
		}
		printSessionStatus("Session:  ", sid)
		fmt.Println()
	}

	a2aURL := buildA2AInvokeURL(rc.projectEndpoint, rc.name, rc.apiVersion, sid)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a2aURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	applyA2ARequestHeaders(req, rc.bearerToken, &a.flags.userIdentityFlags, version)
	if raw {
		// Disable Go's transparent gzip handling so the dumped headers and
		// body match what the server actually sent on the wire.
		req.Header.Set("Accept-Encoding", "identity")
	}

	client := &http.Client{Timeout: a.httpTimeout()}
	invokeStart := time.Now()
	//nolint:gosec // G704: URL is built from a validated Foundry endpoint (env or --agent-endpoint)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s failed: %w", a2aURL, err)
	}
	defer resp.Body.Close()

	// Always capture session state from response headers (needed even in raw mode
	// so subsequent invokes can reuse the session). Reads headers, not the body.
	sessionLabel := "Session:  "
	if raw {
		sessionLabel = ""
	}
	captureResponseSession(ctx, rc.azdClient, agentKey, sid, resp, sessionLabel)

	sessionCode := resp.Header.Get("x-adc-response-details")
	if err := handleA2AResponse(resp, rc.name, raw, version); err != nil {
		if !raw && resp.StatusCode >= 400 {
			a.emitInvokeFailureNextStep(nextstep.InvokeRemote, rc.nextStepName(), sessionCode)
		}
		return err
	}
	totalDuration := time.Since(invokeStart)
	if !raw {
		printInvokeTiming(os.Stdout, totalDuration)
		a.emitInvokeSuccessNextStep(nextstep.InvokeRemote, rc.nextStepName())
	}
	return nil
}

// handleA2AResponse renders the response from a POST to an A2A endpoint. In raw
// mode the response is dumped verbatim (status line + headers + body).
// Otherwise the JSON-RPC response is parsed: a JSON-RPC `error` surfaces as an
// agent error, and the agent's reply text is extracted from the JSON-RPC
// `result` (a Message or Task) and printed. Non-JSON-RPC bodies fall back to
// pretty-printed JSON or the raw body.
func handleA2AResponse(resp *http.Response, agentName string, raw bool, version string) error {
	if raw {
		if err := writeRawResponse(os.Stdout, resp); err != nil {
			return err
		}
		if resp.StatusCode >= 400 {
			return fmt.Errorf(
				"POST %s failed with HTTP %d: %s",
				a2aRequestURL(resp), resp.StatusCode, resp.Status,
			)
		}
		return nil
	}

	if traceID := responseTraceID(resp); traceID != "" {
		fmt.Printf("Trace ID:     %s\n", traceID)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf(
			"POST %s failed with HTTP %d: %s\n%s",
			a2aRequestURL(resp), resp.StatusCode, resp.Status, string(respBody),
		)
	}

	// A JSON-RPC error envelope is an agent-level error.
	if agentErr := a2aErrorFromBody(respBody); agentErr != nil {
		return agentErr
	}

	printA2AResult(respBody, agentName, version)
	return nil
}

// a2aRequestURL returns the request URL of a response for error messages,
// falling back to a generic path when unavailable.
func a2aRequestURL(resp *http.Response) string {
	if resp.Request != nil && resp.Request.URL != nil {
		return resp.Request.URL.String()
	}
	return "/a2a"
}

// a2aErrorFromBody returns a structured error when the response body is a
// JSON-RPC error envelope ({"error":{"code":...,"message":...}}), or nil.
func a2aErrorFromBody(respBody []byte) error {
	var envelope struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return nil
	}
	if envelope.Error == nil {
		return nil
	}
	if envelope.Error.Code != 0 {
		return fmt.Errorf("agent error (%d): %s", envelope.Error.Code, envelope.Error.Message)
	}
	return fmt.Errorf("agent error: %s", envelope.Error.Message)
}

// printA2AResult prints the agent's reply. It extracts the text of the JSON-RPC
// result (a Message or Task); when no text can be found it pretty-prints the
// JSON body, falling back to the raw body for non-JSON.
func printA2AResult(respBody []byte, agentName string, version string) {
	if text := extractA2AText(respBody, version); text != "" {
		fmt.Printf("[%s] %s\n", agentName, text)
		return
	}

	if json.Valid(respBody) {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, respBody, "", "  "); err == nil {
			fmt.Printf("[%s] %s\n", agentName, pretty.String())
			return
		}
	}

	fmt.Printf("[%s] %s\n", agentName, strings.TrimSpace(string(respBody)))
}

// extractA2AText pulls the user-visible reply text out of a JSON-RPC A2A
// response. In 0.3, `result` is a Message or Task. In 1.0, SendMessage wraps
// `message` or `task`, while GetTask and CancelTask return a Task directly.
// Completed Tasks prefer answer artifacts over generic status messages such as "Done".
// Other Tasks prefer status.message so prompts, progress, and failures remain visible.
// Returns "" when no text can be found, signaling the caller to fall back to
// printing the JSON body.
func extractA2AText(respBody []byte, version string) string {
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil || len(envelope.Result) == 0 {
		return ""
	}

	var result map[string]any
	if err := json.Unmarshal(envelope.Result, &result); err != nil {
		return ""
	}

	if version == "1.0" {
		if message, ok := result["message"].(map[string]any); ok {
			return a2aTextFromParts(message["parts"])
		}
		if task, ok := result["task"].(map[string]any); ok {
			result = task
		} else if _, ok := result["status"].(map[string]any); !ok {
			// Only task operations return an unwrapped result in 1.0.
			return ""
		}
	}

	// Message result: parts directly on the object.
	if text := a2aTextFromParts(result["parts"]); text != "" {
		return text
	}

	status, _ := result["status"].(map[string]any)
	var statusText string
	if message, ok := status["message"].(map[string]any); ok {
		statusText = a2aTextFromParts(message["parts"])
	}
	state, _ := status["state"].(string)
	completed := state == "completed" || state == "TASK_STATE_COMPLETED"
	if !completed && statusText != "" {
		return statusText
	}

	// Completed Tasks carry answers in artifacts; their status may only say "Done".
	// Other Tasks also fall back to artifacts when there is no status text.
	if artifacts, ok := result["artifacts"].([]any); ok {
		var parts []string
		for _, art := range artifacts {
			m, ok := art.(map[string]any)
			if !ok {
				continue
			}
			if text := a2aTextFromParts(m["parts"]); text != "" {
				parts = append(parts, text)
			}
		}
		if joined := strings.Join(parts, "\n"); joined != "" {
			return joined
		}
	}

	if statusText != "" {
		return statusText
	}

	// Task result: last agent message in history[].
	if history, ok := result["history"].([]any); ok {
		for i := len(history) - 1; i >= 0; i-- {
			m, ok := history[i].(map[string]any)
			if !ok {
				continue
			}
			if role, _ := m["role"].(string); role == "user" || role == "ROLE_USER" {
				continue
			}
			if text := a2aTextFromParts(m["parts"]); text != "" {
				return text
			}
		}
	}

	return ""
}

// a2aTextFromParts joins the text of every text Part in an A2A parts array.
func a2aTextFromParts(partsAny any) string {
	parts, ok := partsAny.([]any)
	if !ok {
		return ""
	}
	var texts []string
	for _, p := range parts {
		m, ok := p.(map[string]any)
		if !ok {
			continue
		}
		// A2A text parts carry kind:"text" (some emitters use type:"text").
		kind, _ := m["kind"].(string)
		if kind == "" {
			kind, _ = m["type"].(string)
		}
		if kind != "" && kind != "text" {
			// Explicitly a non-text part (file, data, ...).
			continue
		}
		if text, ok := m["text"].(string); ok && strings.TrimSpace(text) != "" {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "\n")
}
