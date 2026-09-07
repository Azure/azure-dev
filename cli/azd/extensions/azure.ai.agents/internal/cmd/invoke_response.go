// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"azureaiagent/internal/cmd/nextstep"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

var errBackgroundNoWait = errors.New("background Response identity saved")

// responseIdentityTracker saves only the current Response identity.
type responseIdentityTracker struct {
	store      responseStateStore
	agentKey   string
	writer     io.Writer
	responseID string
	saveErr    error
	printedID  bool
}

func (t *responseIdentityTracker) Apply(ctx context.Context, responseID string) error {
	if responseID == "" || responseID == t.responseID {
		return nil
	}
	if t.responseID != "" {
		return fmt.Errorf("Responses stream changed response ID from %q to %q", t.responseID, responseID)
	}

	t.responseID = responseID
	if t.store != nil && t.agentKey != "" {
		if err := t.store.Save(ctx, t.agentKey, savedResponse{ResponseID: responseID}); err != nil {
			_, _ = fmt.Fprintf(
				t.writer,
				"Response:     %s\nWARNING: The Response was accepted, but its ID was not saved: %v\n",
				responseID,
				err,
			)
			t.printedID = true
			t.saveErr = fmt.Errorf("save current Response: %w", err)
			return nil
		}
	}
	if !t.printedID {
		if _, err := fmt.Fprintf(t.writer, "Response:     %s\n", responseID); err != nil {
			return err
		}
		t.printedID = true
	}
	return nil
}

func buildResponsesRequestBody(message, sessionID, conversationID string, background bool) map[string]any {
	body := map[string]any{
		"input":        message,
		"stream":       true,
		"conversation": map[string]string{"id": conversationID},
	}
	if sessionID != "" {
		body["agent_session_id"] = sessionID
	}
	if background {
		body["store"] = true
		body["background"] = true
	}
	return body
}

func (a *InvokeAction) responsesRemote(ctx context.Context) error {
	body, bodyLabel, err := a.resolveBody()
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

	agentKey := rc.agentKey
	if agentKey == "" && rc.azdClient != nil {
		log.Printf("warning: agent endpoint not available, session state will not be persisted")
	}

	var responseStore responseStateStore
	if rc.azdClient != nil && agentKey != "" {
		responseStore = newUserConfigResponseStateStore(rc.azdClient)
	}

	// Acquire the bearer token after body validation so a local input error
	// (e.g., unreadable --input-file) does not pay an unnecessary auth round-trip
	// and is surfaced before any auth failure.
	rc.bearerToken, err = a.acquireBearerToken(ctx)
	if err != nil {
		return err
	}

	msg := string(body)

	// Session ID — routes to the same microVM container instance.
	// When empty, let the server assign one.
	sid, err := a.resolveRemoteSessionID(ctx, rc)
	if err != nil {
		return err
	}

	// Conversation ID — enables multi-turn memory via Foundry Conversations API.
	var convID string
	if agentKey != "" && rc.azdClient != nil {
		convID, err = resolveConversationID(
			ctx,
			rc.azdClient,
			agentKey,
			a.flags.conversation,
			a.flags.newConversation,
			rc.projectEndpoint,
			rc.bearerToken,
			rc.name,
			rc.apiVersion,
			a.flags.sessionRequestOptions(),
			rc.legacyKeys()...,
		)
		if err != nil {
			return err
		}
	} else if a.flags.conversation != "" {
		convID = a.flags.conversation
	} else {
		convID, err = createConversation(
			ctx,
			rc.projectEndpoint,
			rc.name,
			rc.bearerToken,
			rc.apiVersion,
			a.flags.sessionRequestOptions(),
		)
		if err != nil {
			return err
		}
	}
	reqBody := buildResponsesRequestBody(msg, sid, convID, a.flags.background)

	raw := a.flags.outputFmt == outputRaw
	if !raw {
		fmt.Printf("Agent:        %s (remote)\n", rc.name)
		fmt.Printf("Message:      %s\n", bodyLabel)
		if rc.version != "" {
			fmt.Printf("Version:      %s\n", rc.version)
		}
		printSessionStatus("Session:      ", sid)
		fmt.Printf("Conversation: %s\n", convID)
		fmt.Println()
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	respURL := buildResponsesURL(rc.projectEndpoint, rc.name, rc.apiVersion)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, respURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	applyCustomHeaders(req, a.clientHeaders)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+rc.bearerToken)
	applyRemoteUserIdentityHeader(req, &a.flags.userIdentityFlags)
	if raw {
		// Disable Go's transparent gzip handling so the dumped headers and
		// body match what the server actually sent on the wire.
		req.Header.Set("Accept-Encoding", "identity")
	}

	client := &http.Client{Timeout: a.httpTimeout()}
	if a.flags.background {
		client = responseStreamHTTPClient()
	}
	invokeStart := time.Now()
	//nolint:gosec // G704: URL is built from a validated Foundry endpoint (env or --agent-endpoint)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s failed: %w", respURL, err)
	}
	ttfb := time.Since(invokeStart)
	defer resp.Body.Close()

	// Always capture session state from response headers (needed even in raw mode
	// so subsequent invokes can reuse the session). Headers are read, not consumed.
	sessionLabel := "Session:      "
	if raw {
		sessionLabel = ""
	}
	captureResponseSession(ctx, rc.azdClient, agentKey, sid, resp, sessionLabel)

	if raw {
		if dumpErr := writeRawResponse(os.Stdout, resp); dumpErr != nil {
			return dumpErr
		}
		if resp.StatusCode >= 400 {
			return fmt.Errorf(
				"POST %s failed with HTTP %d: %s",
				respURL, resp.StatusCode, resp.Status,
			)
		}
		return nil
	}

	if traceID := responseTraceID(resp); traceID != "" {
		fmt.Printf("Trace ID:     %s\n", traceID)
	}

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		a.emitInvokeFailureNextStep(nextstep.InvokeRemote, rc.nextStepName(), resp.Header.Get("x-adc-response-details"))
		return fmt.Errorf("POST %s failed with HTTP %d: %s\n%s", respURL, resp.StatusCode, resp.Status, string(respBody))
	}
	tracker := &responseIdentityTracker{
		store:    responseStore,
		agentKey: agentKey,
		writer:   os.Stdout,
	}
	streamErr := readResponsesSSE(
		ctx,
		resp.Body,
		os.Stdout,
		rc.name,
		responsesSSEOptions{
			requireTerminal: a.flags.background,
			onResponseID: func(responseID string) error {
				if err := tracker.Apply(ctx, responseID); err != nil {
					return err
				}
				if a.flags.noWait && tracker.saveErr != nil {
					return tracker.saveErr
				}
				if a.flags.noWait {
					return errBackgroundNoWait
				}
				return nil
			},
		},
	)
	followCommand := fmt.Sprintf(
		"azd ai agent responses follow --response-id %s",
		tracker.responseID,
	)
	if a.endpoint != nil {
		followCommand += fmt.Sprintf(" --agent-endpoint %q", a.flags.agentEndpoint)
	} else if targetName := rc.nextStepName(); targetName != "" {
		followCommand += fmt.Sprintf(" --agent-name %q", targetName)
	}
	if errors.Is(streamErr, errBackgroundNoWait) {
		fmt.Printf("\nNext:\n  %s\n", followCommand)
		return nil
	}
	if streamErr != nil {
		if a.flags.background && tracker.responseID != "" &&
			errors.Is(streamErr, errResponsesStreamDisconnected) {
			return fmt.Errorf("%w; replay and follow it with `%s`", streamErr, followCommand)
		}
		return streamErr
	}
	totalDuration := time.Since(invokeStart)
	printInvokeTiming(os.Stdout, totalDuration, ttfb)
	a.emitInvokeSuccessNextStep(nextstep.InvokeRemote, rc.nextStepName())
	return nil
}

const maxResponsesSSEEventBytes = 4 * 1024 * 1024

var (
	errResponsesStreamEndedBeforeIdentity = errors.New("Responses stream ended before its identity was received")
	errResponsesStreamDisconnected        = errors.New("Responses stream disconnected before completion")
)

type responsesEventEnvelope struct {
	Type     string          `json:"type"`
	Response json.RawMessage `json:"response"`
}

type responsesSnapshot struct {
	ID             string                `json:"id"`
	ResponseID     string                `json:"response_id"`
	Status         string                `json:"status"`
	AgentSessionID string                `json:"agent_session_id"`
	Output         []responsesOutputItem `json:"output"`
	Error          *responsesError       `json:"error"`
}

type responseSnapshotResult struct {
	snapshot responsesSnapshot
	raw      []byte
}

type responsesOutputItem struct {
	Content []responsesContent `json:"content"`
}

type responsesContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responsesError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type responsesSSEEvent struct {
	name string
	data []byte
}

type responsesSSEOptions struct {
	requireTerminal    bool
	expectedResponseID string
	onResponseID       func(string) error
}

func readResponsesSSE(
	ctx context.Context,
	body io.Reader,
	writer io.Writer,
	agentName string,
	options responsesSSEOptions,
) (returnErr error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxResponsesSSEEventBytes+len("data: ")+2)

	var eventName string
	var eventData bytes.Buffer
	var dataSeen bool
	var printed bool
	identity := options.expectedResponseID
	var status string
	var terminal bool
	defer func() {
		if printed && !terminal {
			_, err := fmt.Fprintln(writer)
			returnErr = errors.Join(returnErr, err)
		}
	}()
	dispatch := func() error {
		if !dataSeen {
			eventName = ""
			return nil
		}

		event := responsesSSEEvent{name: eventName, data: eventData.Bytes()}
		eventName = ""
		dataSeen = false
		defer eventData.Reset()

		var envelope responsesEventEnvelope
		if err := json.Unmarshal(event.data, &envelope); err != nil {
			if event.name == "error" {
				return fmt.Errorf("agent stream error: %s", event.data)
			}
			if event.name != "" && !isKnownResponsesEvent(event.name) {
				return nil
			}
			return fmt.Errorf("decode Responses SSE event: %w", err)
		}
		if event.name == "" {
			event.name = envelope.Type
		}

		previousIdentity := identity
		var snapshot responsesSnapshot
		if len(envelope.Response) > 0 {
			if err := json.Unmarshal(envelope.Response, &snapshot); err != nil {
				return fmt.Errorf("decode Responses snapshot: %w", err)
			}
			responseID := snapshot.ID
			if responseID == "" {
				responseID = snapshot.ResponseID
			}
			if identity != "" && responseID != "" && responseID != identity {
				return fmt.Errorf("Responses stream changed response ID from %q to %q", identity, responseID)
			}
			if responseID != "" {
				identity = responseID
			}
		}
		if previousIdentity == "" && identity != "" && options.onResponseID != nil {
			if err := options.onResponseID(identity); err != nil {
				return err
			}
		}
		if snapshot.Status == "" {
			switch event.name {
			case "response.completed":
				snapshot.Status = "completed"
			case "response.failed":
				snapshot.Status = "failed"
			case "response.incomplete":
				snapshot.Status = "incomplete"
			case "response.cancelled":
				snapshot.Status = "cancelled"
			}
		}
		if options.requireTerminal && identity == "" && isTerminalResponseStatus(snapshot.Status) {
			return errResponsesStreamEndedBeforeIdentity
		}

		switch event.name {
		case "response.output_text.delta":
			var delta struct {
				Delta string `json:"delta"`
			}
			if err := json.Unmarshal(event.data, &delta); err != nil {
				return fmt.Errorf("decode Responses text delta: %w", err)
			}
			if delta.Delta != "" {
				if !printed {
					if _, err := fmt.Fprintf(writer, "[%s] ", agentName); err != nil {
						return err
					}
					printed = true
				}
				if _, err := io.WriteString(writer, delta.Delta); err != nil {
					return err
				}
			}
		case "response.completed", "response.failed", "response.incomplete", "response.cancelled":
			terminal = true
			if printed {
				if _, err := fmt.Fprintln(writer); err != nil {
					return err
				}
			} else if snapshot.Status != "failed" {
				if err := renderResponseSnapshot(writer, agentName, responseSnapshotResult{
					snapshot: snapshot,
					raw:      envelope.Response,
				}); err != nil {
					return err
				}
			}
		case "error":
			var streamErr responsesError
			if err := json.Unmarshal(event.data, &streamErr); err != nil {
				return fmt.Errorf("decode Responses stream error: %w", err)
			}
			return fmt.Errorf("agent error (%s): %s", streamErr.Code, streamErr.Message)
		}

		if snapshot.Status != "" {
			status = snapshot.Status
		}
		if isTerminalResponseStatus(status) {
			terminal = true
		}
		if snapshot.Status == "failed" {
			if snapshot.Error != nil {
				return fmt.Errorf("agent failed (%s): %s", snapshot.Error.Code, snapshot.Error.Message)
			}
			return fmt.Errorf("agent returned failed status")
		}
		if snapshot.Status == "incomplete" && options.requireTerminal {
			return fmt.Errorf("agent returned incomplete status")
		}
		if snapshot.Status == "cancelled" && options.requireTerminal {
			return errors.New("response was cancelled")
		}
		return nil
	}

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := dispatch(); err != nil {
				return err
			}
			if terminal {
				return nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			eventName = value
		case "data":
			addedBytes := len(value)
			if dataSeen {
				addedBytes++
			}
			if addedBytes > maxResponsesSSEEventBytes-eventData.Len() {
				return fmt.Errorf("Responses SSE event exceeds %d bytes", maxResponsesSSEEventBytes)
			}
			if dataSeen {
				eventData.WriteByte('\n')
			}
			eventData.WriteString(value)
			dataSeen = true
		}
	}
	if err := scanner.Err(); err != nil {
		if options.requireTerminal && identity != "" {
			return errors.Join(errResponsesStreamDisconnected, fmt.Errorf("read Responses stream: %w", err))
		}
		return fmt.Errorf("read Responses stream: %w", err)
	}
	if options.requireTerminal && identity == "" {
		return errResponsesStreamEndedBeforeIdentity
	}
	if options.requireTerminal && !terminal {
		return errors.Join(
			errResponsesStreamDisconnected,
			fmt.Errorf("Response %s disconnected before reaching a terminal state", identity),
		)
	}
	return nil
}

func renderResponseSnapshot(
	writer io.Writer,
	agentName string,
	result responseSnapshotResult,
) error {
	var printed bool
	for _, item := range result.snapshot.Output {
		for _, content := range item.Content {
			if content.Type == "output_text" {
				if _, err := fmt.Fprintf(writer, "[%s] %s\n", agentName, content.Text); err != nil {
					return err
				}
				printed = true
			}
		}
	}
	if printed || len(result.raw) == 0 {
		return nil
	}

	var formatted bytes.Buffer
	if err := json.Indent(&formatted, result.raw, "", "  "); err != nil {
		_, err = fmt.Fprintln(writer, string(result.raw))
		return err
	}
	_, err := fmt.Fprintln(writer, formatted.String())
	return err
}

func isKnownResponsesEvent(event string) bool {
	switch event {
	case "response.created", "response.queued", "response.in_progress", "response.output_text.delta",
		"response.completed", "response.failed", "response.incomplete", "response.cancelled", "error":
		return true
	default:
		return false
	}
}

func (a *InvokeAction) responsesLocal(ctx context.Context) error {
	port := a.flags.port

	body, bodyLabel, err := a.resolveBody()
	if err != nil {
		return err
	}

	msg := string(body)

	// Open azd client for session/conversation persistence.
	var azdClient *azdext.AzdClient
	if c, err := azdext.NewAzdClient(); err == nil {
		azdClient = c
		defer azdClient.Close()
	}

	agentKey := resolveLocalAgentKey(ctx, azdClient, a.serviceNameSelector(), a.noPrompt)

	// Resolve local session and conversation IDs (always generated locally).
	var sid, convID string
	if azdClient != nil {
		sid, err = resolveStoredID(
			ctx, azdClient, agentKey, a.flags.session, a.flags.newSession, "sessions", true,
		)
		if err != nil {
			log.Printf("invoke local: failed to resolve session ID: %v", err)
		}
		convID, err = resolveStoredID(
			ctx, azdClient, agentKey, a.flags.conversation, a.flags.newConversation, "conversations", true,
		)
		if err != nil {
			log.Printf("invoke local: failed to resolve conversation ID: %v", err)
		}
	}

	raw := a.flags.outputFmt == outputRaw
	if !raw {
		fmt.Printf("Target:       localhost:%d (local)\n", port)
		fmt.Printf("Message:      %s\n", bodyLabel)
		printSessionStatus("Session:      ", sid)
		fmt.Printf("Conversation: %s\n\n", convID)
	}

	reqBody := map[string]any{
		"input": msg,
	}
	if sid != "" {
		reqBody["session_id"] = sid
	}
	if convID != "" {
		reqBody["conversation"] = map[string]string{"id": convID}
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	reqURL := fmt.Sprintf("http://localhost:%d/responses", port)
	newReq := func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		applyCustomHeaders(req, a.clientHeaders)
		req.Header.Set("Content-Type", "application/json")
		applyLocalUserIdentityHeader(req, &a.flags.userIdentityFlags)
		applyLocalCallIDHeader(req, a.flags.callID)
		if raw {
			// Disable Go's transparent gzip handling so the dumped headers and
			// body match what the server actually sent on the wire.
			req.Header.Set("Accept-Encoding", "identity")
		}
		return req, nil
	}

	client := &http.Client{Timeout: a.httpTimeout()}
	invokeStart := time.Now()
	resp, err := doLocalRequestWithRetry(ctx, client, newReq)
	if err != nil {
		return fmt.Errorf(
			"could not connect to localhost:%d -- is the agent running? Start it with: azd ai agent run",
			port,
		)
	}
	ttfb := time.Since(invokeStart)
	defer resp.Body.Close()

	if raw {
		// Stream the body verbatim to stdout (avoids buffering large responses).
		if dumpErr := writeRawResponse(os.Stdout, resp); dumpErr != nil {
			return dumpErr
		}
		if resp.StatusCode >= 400 {
			return fmt.Errorf(
				"POST %s failed with HTTP %d: %s",
				reqURL, resp.StatusCode, resp.Status,
			)
		}
		return nil
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}
	totalDuration := time.Since(invokeStart)

	if resp.StatusCode >= 400 {
		if traceID := responseTraceID(resp); traceID != "" {
			fmt.Printf("Trace ID:     %s\n", traceID)
		}
		a.emitInvokeFailureNextStep(nextstep.InvokeLocal, "", "")
		return fmt.Errorf(
			"POST %s failed with HTTP %d: %s\n%s",
			reqURL, resp.StatusCode, resp.Status, string(respBody),
		)
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		// Not JSON -- just print raw response
		fmt.Println(string(respBody))
		printInvokeTiming(os.Stdout, totalDuration, ttfb)
		a.emitInvokeSuccessNextStep(nextstep.InvokeLocal, "")
		return nil
	}

	if err := printAgentResponse(result, "local"); err != nil {
		return err
	}
	printInvokeTiming(os.Stdout, totalDuration, ttfb)
	a.emitInvokeSuccessNextStep(nextstep.InvokeLocal, "")
	return nil
}

// remoteContext holds the resolved inputs for a remote (Foundry) invoke.
// In ephemeral mode (--agent-endpoint) the project endpoint / agent name /
// api-version come from the parsed URL.
//
// agentKey is the persistence key used by the global UserConfig store. It is
// non-empty whenever session/conversation IDs should be saved or resumed:
//   - project mode: derived from AGENT_{SVC}_ENDPOINT
//   - ephemeral mode: derived from the parsed --agent-endpoint URL
//     (independent of api-version / trailing slash / fragment)
//
// In standalone mode (no parent azd daemon, e.g. running the extension binary
// directly outside an azd command) azdClient is nil and persistence helpers
// no-op. agentKey may still be non-empty in that case.
// createConversation creates a new Foundry conversation for multi-turn memory.
func createConversation(
	ctx context.Context,
	projectEndpoint, agentName, bearerToken, apiVersion string,
	options *agent_api.SessionRequestOptions,
) (string, error) {
	if apiVersion == "" {
		apiVersion = DefaultAgentAPIVersion
	}
	convURL := fmt.Sprintf(
		"%s/agents/%s/endpoint/protocols/openai/conversations?api-version=%s",
		projectEndpoint, agentName, url.QueryEscape(apiVersion),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, convURL, bytes.NewReader([]byte("{}")))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	options.ApplyHeaders(req.Header)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req) //nolint:gosec // G704: endpoint is resolved from azd environment configuration
	if err != nil {
		return "", fmt.Errorf("POST %s failed: %w", convURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("POST %s failed with HTTP %d: %s\n%s", convURL, resp.StatusCode, resp.Status, string(respBody))
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
