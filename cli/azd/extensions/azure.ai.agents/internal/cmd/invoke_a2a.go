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

	"github.com/google/uuid"
)

// a2aJSONRPCVersion is the JSON-RPC protocol version A2A rides on.
const a2aJSONRPCVersion = "2.0"

// a2aSendMethod is the A2A JSON-RPC method for sending a (non-streaming) message.
// See the A2A protocol spec (https://a2a-protocol.org/latest/specification/).
const a2aSendMethod = "message/send"

// a2aVersionHeader is the A2A protocol version header (§3.2). Sending it makes
// the request's protocol version explicit; if the server does not support it,
// the A2A spec has the agent return VersionNotSupportedError rather than
// silently applying a compatibility default.
const a2aVersionHeader = "A2A-Version"

// a2aProtocolVersion is the A2A protocol version this client speaks. It is a
// constant tied to the request shape we emit (the v0.3 JSON-RPC message/send
// envelope in buildA2ARequestBody), not derived from the agent's advertised
// protocol version: the header must describe the body we actually send. The A2A
// spec treats an omitted version as a 0.3 compatibility default; we send it
// explicitly so a Foundry endpoint on a newer contract negotiates deterministically.
const a2aProtocolVersion = "0.3"

// buildA2ARequestBody returns the JSON request body for an A2A invoke and a
// human-readable label describing it.
//
// When the user supplied --input-file, its bytes are forwarded verbatim — the
// file is assumed to already contain a complete JSON-RPC request (the A2A
// analogue of the invocations protocol's opaque body pass-through). Otherwise
// the plain message is wrapped in a JSON-RPC 2.0 `message/send` request whose
// params carry a single user text Part, per the A2A specification.
//
// NOTE: The A2A request envelope is centralized here so it can be adjusted in
// one place if the Foundry A2A contract differs from the vanilla A2A spec.
func (a *InvokeAction) buildA2ARequestBody() ([]byte, string, error) {
	body, label, err := a.resolveBody()
	if err != nil {
		return nil, "", err
	}
	if a.flags.inputFile != "" {
		// User provided a full JSON-RPC request; forward it unchanged.
		return body, label, nil
	}

	request := map[string]any{
		"jsonrpc": a2aJSONRPCVersion,
		"id":      uuid.NewString(),
		"method":  a2aSendMethod,
		"params": map[string]any{
			"message": map[string]any{
				"role":      "user",
				"messageId": uuid.NewString(),
				"parts": []map[string]any{
					{"kind": "text", "text": string(body)},
				},
			},
		},
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, "", fmt.Errorf("failed to marshal A2A request: %w", err)
	}
	return payload, label, nil
}

// a2aLocal reports that A2A is not supported for local invocation. A2A is a
// remote agent-to-agent protocol; a locally running agent (azd ai agent run)
// does not expose an A2A surface, so we fail fast with actionable guidance
// rather than guessing a local route.
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
func applyA2ARequestHeaders(req *http.Request, bearerToken string, identity *userIdentityFlags) {
	req.Header.Set(a2aVersionHeader, a2aProtocolVersion)
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	applyRemoteUserIdentityHeader(req, identity)
}

// a2aRemote sends the user's message to a deployed Foundry agent using the A2A
// protocol (POST /agents/{name}/endpoint/protocols/a2a) with a JSON-RPC 2.0
// message/send request. Memory is bound to the agent session, like the
// invocations protocol.
func (a *InvokeAction) a2aRemote(ctx context.Context) error {
	rc, err := a.resolveRemoteContext(ctx)
	if err != nil {
		return err
	}
	if rc.azdClient != nil {
		defer rc.azdClient.Close()
	}

	agentKey := rc.agentKey
	if agentKey == "" && rc.azdClient != nil {
		log.Printf("warning: agent endpoint not available, session state will not be persisted")
	}

	if a.flags.newConversation {
		fmt.Fprintln(os.Stderr,
			"note: --new-conversation has no effect for the a2a protocol "+
				"(memory is bound to the session; use --new-session to reset).")
	}

	body, bodyLabel, err := a.buildA2ARequestBody()
	if err != nil {
		return err
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
	applyA2ARequestHeaders(req, rc.bearerToken, &a.flags.userIdentityFlags)
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
	ttfb := time.Since(invokeStart)
	defer resp.Body.Close()

	// Always capture session state from response headers (needed even in raw mode
	// so subsequent invokes can reuse the session). Reads headers, not the body.
	sessionLabel := "Session:  "
	if raw {
		sessionLabel = ""
	}
	captureResponseSession(ctx, rc.azdClient, agentKey, sid, resp, sessionLabel)

	sessionCode := resp.Header.Get("x-adc-response-details")
	if err := handleA2AResponse(resp, rc.name, raw); err != nil {
		if !raw && resp.StatusCode >= 400 {
			a.emitInvokeFailureNextStep(nextstep.InvokeRemote, rc.nextStepName(), sessionCode)
		}
		return err
	}
	totalDuration := time.Since(invokeStart)
	if !raw {
		printInvokeTiming(os.Stdout, totalDuration, ttfb)
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
func handleA2AResponse(resp *http.Response, agentName string, raw bool) error {
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

	printA2AResult(respBody, agentName)
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
func printA2AResult(respBody []byte, agentName string) {
	if text := extractA2AText(respBody); text != "" {
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
// response. The JSON-RPC `result` is either a Message (with `parts`) or a Task
// (whose `status.message`, `artifacts[].parts`, or `history[]` carry the text).
// Returns "" when no text can be found, signaling the caller to fall back to
// printing the JSON body.
func extractA2AText(respBody []byte) string {
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

	// Message result: parts directly on the object.
	if text := a2aTextFromParts(result["parts"]); text != "" {
		return text
	}

	// Task result: status.message.parts.
	if status, ok := result["status"].(map[string]any); ok {
		if msg, ok := status["message"].(map[string]any); ok {
			if text := a2aTextFromParts(msg["parts"]); text != "" {
				return text
			}
		}
	}

	// Task result: artifacts[].parts.
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

	// Task result: last agent message in history[].
	if history, ok := result["history"].([]any); ok {
		for i := len(history) - 1; i >= 0; i-- {
			m, ok := history[i].(map[string]any)
			if !ok {
				continue
			}
			if role, _ := m["role"].(string); role == "user" {
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
