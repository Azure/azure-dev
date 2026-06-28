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
	"net/url"
	"os"
	"strings"
	"time"

	"azureaiagent/internal/cmd/nextstep"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// activityMessageType is the Activity `type` used when wrapping a plain text
// message into a minimal Activity Protocol request body. See
// https://learn.microsoft.com/microsoft-365/agents-sdk/activity-protocol.
const activityMessageType = "message"

// buildActivityRequestBody returns the JSON request body for an activity-protocol
// invoke and a human-readable label describing it.
//
// When the user supplied --input-file, its bytes are forwarded verbatim — the
// file is assumed to already contain a complete Activity object, mirroring the
// invocations protocol's opaque body pass-through. Otherwise the plain message
// is wrapped into a minimal `message` Activity ({"type":"message","text":...}).
//
// Session routing is handled out-of-band via the agent_session_id query
// parameter (see buildActivityURL / activityLocal), matching the invocations
// protocol, so no session/conversation id is injected into the body here.
func (a *InvokeAction) buildActivityRequestBody() ([]byte, string, error) {
	body, label, err := a.resolveBody()
	if err != nil {
		return nil, "", err
	}
	if a.flags.inputFile != "" {
		// User provided a full Activity payload; forward it unchanged.
		return body, label, nil
	}

	activity := map[string]any{
		"type": activityMessageType,
		"text": string(body),
	}
	payload, err := json.Marshal(activity)
	if err != nil {
		return nil, "", fmt.Errorf("failed to marshal activity request: %w", err)
	}
	return payload, label, nil
}

// activityLocal invokes a locally running agent (started via 'azd ai agent run')
// over the activity protocol (POST http://localhost:{port}/activity).
func (a *InvokeAction) activityLocal(ctx context.Context) error {
	port := a.flags.port

	body, bodyLabel, err := a.buildActivityRequestBody()
	if err != nil {
		return err
	}

	var azdClient *azdext.AzdClient
	if c, err := azdext.NewAzdClient(); err == nil {
		azdClient = c
		defer azdClient.Close()
	}

	agentName := resolveLocalAgentName(ctx, azdClient, a.flags.name, a.noPrompt)
	// The session-storage key intentionally uses DefaultPort (not a.flags.port),
	// matching invocationsLocal: keying on a stable port lets local sessions
	// persist across invokes regardless of which --port the user passes.
	agentKey := buildLocalAgentKey(DefaultPort, agentName, "", resolveProjectPath(ctx, azdClient))

	// Resolve local session ID (generated locally, not server-assigned).
	var sid string
	if azdClient != nil {
		sid, err = resolveStoredID(
			ctx, azdClient, agentKey, a.flags.session, a.flags.newSession, "sessions", true,
		)
		if err != nil {
			log.Printf("invoke local: failed to resolve session ID: %v", err)
		}
	}

	// Note: unlike invocationsLocal, the activity protocol has no defined
	// OpenAPI discovery endpoint, so no spec is fetched/cached here.

	raw := a.flags.outputFmt == outputRaw
	if !raw {
		fmt.Printf("Target:   localhost:%d (local, activity protocol)\n", port)
		fmt.Printf("Input:    %s\n", bodyLabel)
		printSessionStatus("Session:  ", sid)
		fmt.Println()
	}

	actURL := fmt.Sprintf("http://localhost:%d/activity", port)
	if sid != "" {
		actURL += "?agent_session_id=" + url.QueryEscape(sid)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, actURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", contentTypeForBody(body))
	if raw {
		// Disable Go's transparent gzip handling so the dumped headers and
		// body match what the server actually sent on the wire.
		req.Header.Set("Accept-Encoding", "identity")
	}

	client := &http.Client{Timeout: a.httpTimeout()}
	invokeStart := time.Now()
	resp, err := client.Do(req) //nolint:gosec // G704: URL targets localhost with user-configured port
	if err != nil {
		return fmt.Errorf(
			"could not connect to localhost:%d -- is the agent running? Start it with: azd ai agent run",
			port,
		)
	}
	ttfb := time.Since(invokeStart)
	defer resp.Body.Close()

	if err := handleActivityResponse(resp, agentName, raw); err != nil {
		if !raw && resp.StatusCode >= 400 {
			a.emitInvokeFailureNextStep(nextstep.InvokeLocal, agentName, "")
		}
		return err
	}
	totalDuration := time.Since(invokeStart)
	if !raw {
		printInvokeTiming(os.Stdout, totalDuration, ttfb)
		a.emitInvokeSuccessNextStep(nextstep.InvokeLocal, agentName)
	}
	return nil
}

// activityRemote sends the user's message to Foundry using the activity protocol
// (POST /agents/{name}/endpoint/protocols/activity). Memory is bound to the
// agent session, like the invocations protocol.
func (a *InvokeAction) activityRemote(ctx context.Context) error {
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
			"note: --new-conversation has no effect for the activity protocol "+
				"(memory is bound to the session; use --new-session to reset).")
	}

	body, bodyLabel, err := a.buildActivityRequestBody()
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
		fmt.Printf("Agent:    %s (remote, activity protocol)\n", rc.name)
		fmt.Printf("Input:    %s\n", bodyLabel)
		if rc.version != "" {
			fmt.Printf("Version:  %s\n", rc.version)
		}
		printSessionStatus("Session:  ", sid)
		fmt.Println()
	}

	actURL := buildActivityURL(rc.projectEndpoint, rc.name, rc.apiVersion, sid)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, actURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", contentTypeForBody(body))
	req.Header.Set("Authorization", "Bearer "+rc.bearerToken)
	req.Header.Set("Foundry-Features", "HostedAgents=V1Preview")
	applyIsolationHeaders(req, &a.flags.isolationHeaderFlags)
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
		return fmt.Errorf("POST %s failed: %w", actURL, err)
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
	if err := handleActivityResponse(resp, rc.name, raw); err != nil {
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

// handleActivityResponse renders the response from a POST to an activity-protocol
// endpoint. In raw mode the response is dumped verbatim (status line + headers +
// body). Otherwise the agent's reply text is extracted from the response Activity
// and printed; non-Activity JSON is pretty-printed, and any other body is printed
// verbatim.
func handleActivityResponse(resp *http.Response, agentName string, raw bool) error {
	if raw {
		if err := writeRawResponse(os.Stdout, resp); err != nil {
			return err
		}
		if resp.StatusCode >= 400 {
			return fmt.Errorf(
				"POST %s failed with HTTP %d: %s",
				activityRequestURL(resp), resp.StatusCode, resp.Status,
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
			activityRequestURL(resp), resp.StatusCode, resp.Status, string(respBody),
		)
	}

	// Surface an agent error envelope if present, matching the invocations
	// protocol's behavior.
	if agentErr := activityErrorFromBody(respBody); agentErr != nil {
		return agentErr
	}

	printActivityResult(respBody, agentName)
	return nil
}

// activityRequestURL returns the request URL of a response for error messages,
// falling back to a generic path when unavailable.
func activityRequestURL(resp *http.Response) string {
	if resp.Request != nil && resp.Request.URL != nil {
		return resp.Request.URL.String()
	}
	return "/activity"
}

// activityErrorFromBody returns a structured error when the response body is a
// recommended agent error envelope ({"error":{"message":...}}), or nil otherwise.
func activityErrorFromBody(respBody []byte) error {
	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil
	}
	errObj, ok := result["error"].(map[string]any)
	if !ok {
		return nil
	}
	msg, _ := errObj["message"].(string)
	errType, _ := errObj["type"].(string)
	code, _ := errObj["code"].(string)
	label := code
	if label == "" {
		label = errType
	}
	if label != "" {
		return fmt.Errorf("agent error (%s): %s", label, msg)
	}
	return fmt.Errorf("agent error: %s", msg)
}

// printActivityResult prints the agent's reply. It first tries to extract the
// text of the response Activity (or Activities); when no text can be found it
// pretty-prints the JSON body, falling back to the raw body for non-JSON.
func printActivityResult(respBody []byte, agentName string) {
	if text := extractActivityText(respBody); text != "" {
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

// extractActivityText pulls the user-visible message text out of an activity
// protocol response. It accepts the common shapes returned by activity
// endpoints:
//
//   - a single Activity object with a top-level "text"
//   - an InvokeResponse-style envelope {"body": {"text": ...}}
//   - an array of Activities (the message activities' text is joined)
//
// It returns "" when no text can be found, signaling the caller to fall back to
// printing the JSON body.
func extractActivityText(respBody []byte) string {
	var asObject map[string]any
	if err := json.Unmarshal(respBody, &asObject); err == nil {
		if text := activityTextFromObject(asObject); text != "" {
			return text
		}
		return ""
	}

	var asArray []map[string]any
	if err := json.Unmarshal(respBody, &asArray); err == nil {
		var parts []string
		for _, item := range asArray {
			if text := activityTextFromObject(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	}

	return ""
}

// activityTextFromObject extracts the message text from a single Activity-shaped
// map, checking the top-level "text" first and then an InvokeResponse "body".
func activityTextFromObject(obj map[string]any) string {
	if text, ok := obj["text"].(string); ok && strings.TrimSpace(text) != "" {
		return text
	}
	if body, ok := obj["body"].(map[string]any); ok {
		if text, ok := body["text"].(string); ok && strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}
