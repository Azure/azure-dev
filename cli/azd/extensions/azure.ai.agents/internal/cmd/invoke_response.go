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
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"azureaiagent/internal/exterrors"

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

// Apply saves and prints the first discovered Response ID.
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

// buildResponsesRequestBody creates the streaming Responses request payload.
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

// responseLifecycleCommand formats guidance for current or explicitly targeted work.
func (a *InvokeAction) responseLifecycleCommand(
	rc *remoteContext, id string, operation invocationOperation, useCurrent bool,
) string {
	command := "azd ai agent invocations " + string(operation)
	if !useCurrent || a.endpoint != nil {
		command += fmt.Sprintf(" --id %q", id)
	}
	if a.endpoint != nil {
		return command + fmt.Sprintf(" --agent-endpoint %q", a.flags.agentEndpoint)
	}
	command += " --protocol responses"
	if name := rc.nextStepName(); name != "" {
		command += fmt.Sprintf(" --agent-name %q", name)
	}
	return command
}

// runResponseOperation implements lifecycle operations for the Responses protocol.
func (a *InvokeAction) runResponseOperation(
	ctx context.Context,
	rc *remoteContext,
	id string,
	operation invocationOperation,
	format string,
	writer io.Writer,
) error {
	switch operation {
	case invocationShow:
		result, err := a.getResponseSnapshot(ctx, rc, id)
		if err != nil {
			return classifyResponseLifecycleError(err, exterrors.OpShowResponse, "showing Response", "")
		}
		return printResponseSnapshot(writer, result, format)
	case invocationFollow:
		return classifyResponseLifecycleError(
			a.followResponse(ctx, rc, id, writer), exterrors.OpFollowResponse, "following Response",
			a.responseLifecycleCommand(rc, id, invocationShow, false),
		)
	case invocationCancel:
		return classifyResponseLifecycleError(
			a.cancelResponse(ctx, rc, id, writer), exterrors.OpCancelResponse, "cancelling Response", "",
		)
	default:
		return fmt.Errorf("unsupported Responses operation %q", operation)
	}
}

// classifyResponseStateReadError adds guidance to saved-state failures.
func classifyResponseStateReadError(cause error) error {
	if _, ok := errors.AsType[*azdext.ConfigError](cause); !ok {
		return exterrors.FromHost(cause, exterrors.OpReadResponseState, "reading current Response state failed")
	}
	return exterrors.Validation(
		exterrors.CodeInvalidResponseState,
		fmt.Sprintf("saved Response state at %q could not be read: %v", responsesConfigPath, cause),
		fmt.Sprintf(
			"clear the invalid state with `azd config unset %s`, or repair that config value",
			responsesConfigPath,
		),
	)
}

// classifyResponseLifecycleError translates HTTP failures into structured service errors.
func classifyResponseLifecycleError(cause error, operation, label, showCommand string) error {
	if cause == nil {
		return nil
	}
	httpErr, ok := errors.AsType[*responseLifecycleHTTPError](cause)
	if !ok {
		return cause
	}
	serviceName := ""
	if parsed, err := url.Parse(httpErr.requestURL); err == nil {
		serviceName = parsed.Hostname()
	}
	serviceErr := exterrors.Service(
		operation,
		strconv.Itoa(httpErr.statusCode),
		fmt.Sprintf("%s failed with HTTP %d: %s", label, httpErr.statusCode, httpErr.status),
		serviceName,
		"",
	)
	serviceErr.StatusCode = httpErr.statusCode
	if operation == exterrors.OpFollowResponse && httpErr.statusCode == http.StatusBadRequest {
		var body struct {
			Error struct {
				Code    string `json:"code"`
				Param   string `json:"param"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(httpErr.body, &body) == nil &&
			body.Error.Code == "invalid_request_error" && body.Error.Param == "stream" {
			switch body.Error.Message {
			case "This response cannot be streamed because it was not created with stream=true " +
				"or the stream TTL has expired.":
				// The service also returns this for cancelled/failed work without a stream available for replay.
				// It does not prove cancellation, so do not infer a lifecycle status from this error.
				serviceErr.Message = "Output is unavailable for this invocation. " +
					"Its stream was not recorded or has expired."
				serviceErr.Suggestion = fmt.Sprintf("run `%s` to inspect its current state", showCommand)
				return serviceErr
			case "This response cannot be streamed because it was not created with background=true.":
				serviceErr.Message = "This invocation cannot be followed because it was not started with --long-running."
				serviceErr.Suggestion = fmt.Sprintf(
					"run `%s` to inspect the result; use `azd ai agent invoke --long-running` for work you want to follow later",
					showCommand,
				)
				return serviceErr
			}
		}
	}
	if cause.Error() != httpErr.Error() {
		serviceErr.Suggestion = "use `azd ai agent invocations show --protocol responses` to inspect the Response, or run the follow command again"
	}
	return serviceErr
}

// cancelResponse requests cancellation and confirms terminal state on rejection.
func (a *InvokeAction) cancelResponse(
	ctx context.Context,
	rc *remoteContext,
	responseID string,
	writer io.Writer,
) error {
	token, err := a.acquireBearerToken(ctx)
	if err != nil {
		return err
	}
	cancelURL := buildResponseCancelURL(rc.projectEndpoint, rc.name, responseID, rc.apiVersion)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cancelURL, nil)
	if err != nil {
		return fmt.Errorf("create Response cancel request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	applyRemoteUserIdentityHeader(req, &a.flags.userIdentityFlags)

	//nolint:gosec // URL is built from a validated Foundry endpoint.
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("cancel Response %s: %w", responseID, err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return fmt.Errorf("read Response cancel result: %w", readErr)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		// Cancellation is idempotent from the CLI perspective. Confirm a terminal
		// state from the service rather than trusting stale local status.
		result, snapshotErr := a.getResponseSnapshot(ctx, rc, responseID)
		if snapshotErr == nil && isTerminalResponseStatus(result.snapshot.Status) {
			_, err = fmt.Fprintf(
				writer, "Response %s is already %s; nothing to cancel.\n", responseID, result.snapshot.Status,
			)
			return err
		}
		return &responseLifecycleHTTPError{
			method:     http.MethodPost,
			requestURL: cancelURL,
			statusCode: resp.StatusCode,
			status:     resp.Status,
			body:       body,
		}
	}

	result, decodeErr := decodeResponseSnapshot(body)
	if decodeErr == nil && result.Status != "" {
		_, err = fmt.Fprintf(writer, "Response %s is %s.\n", responseID, result.Status)
		return err
	}
	_, err = fmt.Fprintf(writer, "Cancellation requested for Response %s.\n", responseID)
	return err
}

// getResponseSnapshot retrieves a snapshot and validates its identity.
func (a *InvokeAction) getResponseSnapshot(
	ctx context.Context,
	rc *remoteContext,
	responseID string,
) (responseSnapshotResult, error) {
	token, err := a.acquireBearerToken(ctx)
	if err != nil {
		return responseSnapshotResult{}, err
	}
	snapshotURL := buildResponseLifecycleURL(rc.projectEndpoint, rc.name, responseID, rc.apiVersion, false)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, snapshotURL, nil)
	if err != nil {
		return responseSnapshotResult{}, fmt.Errorf("create Response show request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	applyRemoteUserIdentityHeader(req, &a.flags.userIdentityFlags)

	//nolint:gosec // URL is built from a validated Foundry endpoint.
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return responseSnapshotResult{}, fmt.Errorf("show Response %s: %w", responseID, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return responseSnapshotResult{}, fmt.Errorf("read Response snapshot: %w", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return responseSnapshotResult{}, &responseLifecycleHTTPError{
			method:     http.MethodGet,
			requestURL: snapshotURL,
			statusCode: resp.StatusCode,
			status:     resp.Status,
			body:       body,
		}
	}
	snapshot, err := decodeResponseSnapshot(body)
	if err != nil {
		return responseSnapshotResult{}, fmt.Errorf("decode Response snapshot: %w", err)
	}
	actualID := snapshot.ID
	if actualID == "" {
		actualID = snapshot.ResponseID
	}
	if actualID != "" && actualID != responseID {
		return responseSnapshotResult{}, fmt.Errorf(
			"Response snapshot ID %q does not match requested ID %q", actualID, responseID,
		)
	}
	return responseSnapshotResult{snapshot: snapshot, raw: body}, nil
}

// decodeResponseSnapshot decodes a service Response object.
func decodeResponseSnapshot(body []byte) (responsesSnapshot, error) {
	var snapshot responsesSnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return responsesSnapshot{}, err
	}
	return snapshot, nil
}

// printResponseSnapshot writes a Response as JSON or a table.
func printResponseSnapshot(writer io.Writer, result responseSnapshotResult, format string) error {
	if format != "table" {
		var formatted any
		if err := json.Unmarshal(result.raw, &formatted); err != nil {
			return fmt.Errorf("decode Response JSON: %w", err)
		}
		data, err := json.MarshalIndent(formatted, "", "  ")
		if err != nil {
			return fmt.Errorf("format Response JSON: %w", err)
		}
		_, err = fmt.Fprintln(writer, string(data))
		return err
	}

	id := result.snapshot.ID
	if id == "" {
		id = result.snapshot.ResponseID
	}
	table := tabwriter.NewWriter(writer, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "FIELD\tVALUE")
	fmt.Fprintln(table, "-----\t-----")
	fmt.Fprintf(table, "Response ID\t%s\n", id)
	fmt.Fprintf(table, "Status\t%s\n", result.snapshot.Status)
	fmt.Fprintf(table, "Session ID\t%s\n", result.snapshot.AgentSessionID)
	return table.Flush()
}

func isTerminalResponseStatus(status string) bool {
	switch status {
	case "completed", "failed", "incomplete", "cancelled":
		return true
	default:
		return false
	}
}

// followResponse performs one streaming GET. A later command replays the
// Response from the beginning; azd does not maintain a replay cursor.
func (a *InvokeAction) followResponse(
	ctx context.Context,
	rc *remoteContext,
	responseID string,
	writer io.Writer,
) error {
	token, err := a.acquireBearerToken(ctx)
	if err != nil {
		return err
	}
	followURL := buildResponseLifecycleURL(
		rc.projectEndpoint,
		rc.name,
		responseID,
		rc.apiVersion,
		true,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, followURL, nil)
	if err != nil {
		return fmt.Errorf("create Response follow request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")
	applyRemoteUserIdentityHeader(req, &a.flags.userIdentityFlags)

	//nolint:gosec // URL is built from a validated Foundry endpoint.
	resp, err := responseStreamHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("follow Response %s: %w", responseID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		return &responseLifecycleHTTPError{
			method:     http.MethodGet,
			requestURL: followURL,
			statusCode: resp.StatusCode,
			status:     resp.Status,
			body:       body,
		}
	}

	if err := readResponsesSSE(
		ctx,
		resp.Body,
		writer,
		rc.name,
		responsesSSEOptions{
			requireTerminal:    true,
			expectedResponseID: responseID,
		},
	); err != nil {
		if errors.Is(err, errResponsesStreamDisconnected) {
			return fmt.Errorf(
				"%w; rerun `%s` to replay and follow again",
				err,
				a.responseLifecycleCommand(rc, responseID, invocationFollow, false),
			)
		}
		return err
	}
	return nil
}

type responseLifecycleHTTPError struct {
	method     string
	requestURL string
	statusCode int
	status     string
	body       []byte
}

// Error describes the failed lifecycle HTTP request.
func (e *responseLifecycleHTTPError) Error() string {
	return fmt.Sprintf("%s %s failed with HTTP %d: %s\n%s", e.method, e.requestURL, e.statusCode, e.status, e.body)
}

// responseStreamHTTPClient bounds header acquisition without limiting stream duration.
func responseStreamHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	return &http.Client{Transport: transport}
}

// buildResponseLifecycleURL builds the snapshot or streaming GET URL.
func buildResponseLifecycleURL(
	projectEndpoint string,
	agentName string,
	responseID string,
	apiVersion string,
	stream bool,
) string {
	if apiVersion == "" {
		apiVersion = DefaultAgentAPIVersion
	}
	base := fmt.Sprintf(
		"%s/agents/%s/endpoint/protocols/openai/responses/%s",
		projectEndpoint,
		agentName,
		url.PathEscape(responseID),
	)
	query := url.Values{"api-version": []string{apiVersion}}
	if stream {
		query.Set("stream", "true")
	}
	return base + "?" + query.Encode()
}

// buildResponseCancelURL builds the Response cancellation URL.
func buildResponseCancelURL(projectEndpoint, agentName, responseID, apiVersion string) string {
	lifecycleURL := buildResponseLifecycleURL(projectEndpoint, agentName, responseID, apiVersion, false)
	parts := strings.SplitN(lifecycleURL, "?", 2)
	return parts[0] + "/cancel?" + parts[1]
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
			return errors.New("this invocation was cancelled")
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

const responsesConfigPath = configPathPrefix + ".responses"

type savedResponse struct {
	ResponseID string `json:"responseId"`
}

type responseStateStore interface {
	Get(ctx context.Context, agentKey string) (*savedResponse, error)
	Save(ctx context.Context, agentKey string, record savedResponse) error
	Delete(ctx context.Context, agentKey string) error
}

type userConfigResponseStateStore struct {
	client *azdext.AzdClient
}

func newUserConfigResponseStateStore(client *azdext.AzdClient) responseStateStore {
	return &userConfigResponseStateStore{client: client}
}

func (s *userConfigResponseStateStore) Get(ctx context.Context, agentKey string) (*savedResponse, error) {
	config, err := azdext.NewConfigHelper(s.client)
	if err != nil {
		return nil, fmt.Errorf("create response config helper: %w", err)
	}

	var records map[string]savedResponse
	found, err := config.GetUserJSON(ctx, responsesConfigPath, &records)
	if err != nil {
		return nil, fmt.Errorf("read responses: %w", err)
	}
	if !found || records == nil {
		return nil, nil
	}
	record, ok := records[agentKey]
	if !ok {
		return nil, nil
	}
	return &record, nil
}

func (s *userConfigResponseStateStore) Save(ctx context.Context, agentKey string, record savedResponse) error {
	config, err := azdext.NewConfigHelper(s.client)
	if err != nil {
		return fmt.Errorf("create response config helper: %w", err)
	}

	var records map[string]savedResponse
	found, err := config.GetUserJSON(ctx, responsesConfigPath, &records)
	if err != nil {
		return fmt.Errorf("read responses: %w", err)
	}
	if !found || records == nil {
		records = make(map[string]savedResponse)
	}
	records[agentKey] = record

	if err := config.SetUserJSON(ctx, responsesConfigPath, records); err != nil {
		return fmt.Errorf("write responses: %w", err)
	}
	return nil
}

func (s *userConfigResponseStateStore) Delete(ctx context.Context, agentKey string) error {
	config, err := azdext.NewConfigHelper(s.client)
	if err != nil {
		return fmt.Errorf("create response config helper: %w", err)
	}

	var records map[string]savedResponse
	found, err := config.GetUserJSON(ctx, responsesConfigPath, &records)
	if err != nil {
		return fmt.Errorf("read responses: %w", err)
	}
	if !found || records == nil {
		return nil
	}
	delete(records, agentKey)
	if err := config.SetUserJSON(ctx, responsesConfigPath, records); err != nil {
		return fmt.Errorf("write responses: %w", err)
	}
	return nil
}
