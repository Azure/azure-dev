// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
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

func (a *InvokeAction) responseLifecycleCommand(
	rc *remoteContext, id string, operation invocationOperation, useCurrent bool,
) string {
	command := "azd ai agent invocations " + string(operation)
	if useCurrent && a.endpoint == nil {
		return command
	}
	command += fmt.Sprintf(" --id %q", id)
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
				// The service also returns this for cancelled/failed work without a replayable stream.
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
	applyCustomHeaders(req, a.clientHeaders)
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
	applyCustomHeaders(req, a.clientHeaders)
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

func decodeResponseSnapshot(body []byte) (responsesSnapshot, error) {
	var snapshot responsesSnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return responsesSnapshot{}, err
	}
	return snapshot, nil
}

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
	applyCustomHeaders(req, a.clientHeaders)
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

func (e *responseLifecycleHTTPError) Error() string {
	return fmt.Sprintf("%s %s failed with HTTP %d: %s\n%s", e.method, e.requestURL, e.statusCode, e.status, e.body)
}

func responseStreamHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	return &http.Client{Transport: transport}
}

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

func buildResponseCancelURL(projectEndpoint, agentName, responseID, apiVersion string) string {
	lifecycleURL := buildResponseLifecycleURL(projectEndpoint, agentName, responseID, apiVersion, false)
	parts := strings.SplitN(lifecycleURL, "?", 2)
	return parts[0] + "/cancel?" + parts[1]
}
