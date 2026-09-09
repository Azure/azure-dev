// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"text/tabwriter"
	"time"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func invocationIDFromResponse(resp *http.Response) (string, error) {
	if invocationID := resp.Header.Get("x-agent-invocation-id"); invocationID != "" {
		return invocationID, nil
	}
	if resp.StatusCode != http.StatusAccepted {
		return "", nil
	}

	originalBody := resp.Body
	body, err := io.ReadAll(originalBody)
	_ = originalBody.Close()
	if err != nil {
		return "", fmt.Errorf("read accepted Invocation response: %w", err)
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	var accepted struct {
		InvocationID string `json:"invocation_id"`
	}
	if err := json.Unmarshal(body, &accepted); err != nil {
		return "", nil
	}
	return accepted.InvocationID, nil
}

type invocationSnapshot struct {
	ID           string `json:"id"`
	InvocationID string `json:"invocation_id"`
	Status       string `json:"status"`
}

type invocationSnapshotResult struct {
	snapshot invocationSnapshot
	raw      []byte
}

func (a *InvokeAction) runInvocationsProtocolOperation(
	ctx context.Context,
	rc *remoteContext,
	id string,
	operation invocationOperation,
	format string,
	writer io.Writer,
) error {
	switch operation {
	case invocationShow:
		result, err := a.getInvocation(ctx, rc, id)
		if err != nil {
			return classifyInvocationLifecycleError(err, exterrors.OpShowInvocation, "showing Invocation")
		}
		return printInvocationSnapshot(writer, result, format)
	case invocationCancel:
		return classifyInvocationLifecycleError(
			a.cancelInvocation(ctx, rc, id, writer), exterrors.OpCancelInvocation, "cancelling Invocation",
		)
	default:
		return exterrors.Validation(exterrors.CodeInvalidParameter,
			fmt.Sprintf("invocations %s is not supported with the invocations protocol", operation),
			"use invocations show to retrieve a snapshot")
	}
}

func classifyInvocationStateReadError(cause error) error {
	if _, ok := errors.AsType[*azdext.ConfigError](cause); !ok {
		return exterrors.FromHost(cause, exterrors.OpReadInvocationState, "reading current Invocation state failed")
	}
	return exterrors.Validation(
		exterrors.CodeInvalidInvocationState,
		fmt.Sprintf("saved Invocation state at %q could not be read: %v", invocationsConfigPath, cause),
		fmt.Sprintf("clear the invalid state with `azd config unset %s`, or repair that config value", invocationsConfigPath),
	)
}

func (a *InvokeAction) cancelInvocation(
	ctx context.Context, rc *remoteContext, invocationID string, writer io.Writer,
) error {
	token, err := a.acquireBearerToken(ctx)
	if err != nil {
		return err
	}
	cancelURL := buildInvocationCancelURL(rc.projectEndpoint, rc.name, invocationID, rc.apiVersion)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cancelURL, nil)
	if err != nil {
		return fmt.Errorf("create Invocation cancel request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	applyCustomHeaders(req, a.clientHeaders)
	applyRemoteUserIdentityHeader(req, &a.flags.userIdentityFlags)

	//nolint:gosec // URL is built from a validated Foundry endpoint.
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("cancel Invocation %s: %w", invocationID, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read Invocation cancel result: %w", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		result, getErr := a.getInvocation(ctx, rc, invocationID)
		if getErr == nil && isTerminalInvocationStatus(result.snapshot.Status) {
			_, err = fmt.Fprintf(
				writer,
				"Invocation %s is already %s; nothing to cancel.\n",
				invocationID,
				result.snapshot.Status,
			)
			return err
		}
		return &invocationLifecycleHTTPError{
			method:     http.MethodPost,
			requestURL: cancelURL,
			statusCode: resp.StatusCode,
			status:     resp.Status,
			body:       body,
		}
	}

	var result invocationSnapshot
	if json.Unmarshal(body, &result) == nil && result.Status != "" {
		_, err = fmt.Fprintf(writer, "Invocation %s is %s.\n", invocationID, result.Status)
		return err
	}
	_, err = fmt.Fprintf(writer, "Cancellation requested for Invocation %s.\n", invocationID)
	return err
}

func (a *InvokeAction) getInvocation(
	ctx context.Context,
	rc *remoteContext,
	invocationID string,
) (invocationSnapshotResult, error) {
	token, err := a.acquireBearerToken(ctx)
	if err != nil {
		return invocationSnapshotResult{}, err
	}
	requestURL := buildInvocationLifecycleURL(rc.projectEndpoint, rc.name, invocationID, rc.apiVersion)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return invocationSnapshotResult{}, fmt.Errorf("create Invocation show request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	applyCustomHeaders(req, a.clientHeaders)
	applyRemoteUserIdentityHeader(req, &a.flags.userIdentityFlags)

	//nolint:gosec // URL is built from a validated Foundry endpoint.
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return invocationSnapshotResult{}, fmt.Errorf("show Invocation %s: %w", invocationID, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return invocationSnapshotResult{}, fmt.Errorf("read Invocation: %w", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return invocationSnapshotResult{}, &invocationLifecycleHTTPError{
			method:     http.MethodGet,
			requestURL: requestURL,
			statusCode: resp.StatusCode,
			status:     resp.Status,
			body:       body,
		}
	}
	var snapshot invocationSnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return invocationSnapshotResult{}, fmt.Errorf("decode Invocation: %w", err)
	}
	actualID := snapshot.ID
	if actualID == "" {
		actualID = snapshot.InvocationID
	}
	if actualID != "" && actualID != invocationID {
		return invocationSnapshotResult{}, fmt.Errorf(
			"Invocation ID %q does not match requested ID %q",
			actualID,
			invocationID,
		)
	}
	return invocationSnapshotResult{snapshot: snapshot, raw: body}, nil
}

type invocationLifecycleHTTPError struct {
	method     string
	requestURL string
	statusCode int
	status     string
	body       []byte
}

func (e *invocationLifecycleHTTPError) Error() string {
	return fmt.Sprintf("%s %s failed with HTTP %d: %s\n%s", e.method, e.requestURL, e.statusCode, e.status, e.body)
}

func classifyInvocationLifecycleError(cause error, operation, label string) error {
	if cause == nil {
		return nil
	}
	httpErr, ok := errors.AsType[*invocationLifecycleHTTPError](cause)
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
	return serviceErr
}

func printInvocationSnapshot(writer io.Writer, result invocationSnapshotResult, format string) error {
	if format != "table" {
		var formatted any
		if err := json.Unmarshal(result.raw, &formatted); err != nil {
			return fmt.Errorf("decode Invocation JSON: %w", err)
		}
		data, err := json.MarshalIndent(formatted, "", "  ")
		if err != nil {
			return fmt.Errorf("format Invocation JSON: %w", err)
		}
		_, err = fmt.Fprintln(writer, string(data))
		return err
	}

	id := result.snapshot.ID
	if id == "" {
		id = result.snapshot.InvocationID
	}
	table := tabwriter.NewWriter(writer, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "FIELD\tVALUE")
	fmt.Fprintln(table, "-----\t-----")
	fmt.Fprintf(table, "Invocation ID\t%s\n", id)
	fmt.Fprintf(table, "Status\t%s\n", result.snapshot.Status)
	return table.Flush()
}

func isTerminalInvocationStatus(status string) bool {
	switch status {
	case "completed", "failed", "cancelled", "canceled":
		return true
	default:
		return false
	}
}

const invocationsConfigPath = configPathPrefix + ".invocations"

type savedInvocation struct {
	InvocationID string `json:"invocationId"`
}

type invocationStateStore struct {
	client *azdext.AzdClient
}

func newInvocationStateStore(client *azdext.AzdClient) *invocationStateStore {
	return &invocationStateStore{client: client}
}

func (s *invocationStateStore) Get(ctx context.Context, agentKey string) (*savedInvocation, error) {
	config, err := azdext.NewConfigHelper(s.client)
	if err != nil {
		return nil, fmt.Errorf("create invocation config helper: %w", err)
	}
	var records map[string]savedInvocation
	found, err := config.GetUserJSON(ctx, invocationsConfigPath, &records)
	if err != nil {
		return nil, fmt.Errorf("read invocations: %w", err)
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

func (s *invocationStateStore) Save(ctx context.Context, agentKey string, record savedInvocation) error {
	config, err := azdext.NewConfigHelper(s.client)
	if err != nil {
		return fmt.Errorf("create invocation config helper: %w", err)
	}
	var records map[string]savedInvocation
	found, err := config.GetUserJSON(ctx, invocationsConfigPath, &records)
	if err != nil {
		return fmt.Errorf("read invocations: %w", err)
	}
	if !found || records == nil {
		records = make(map[string]savedInvocation)
	}
	records[agentKey] = record
	if err := config.SetUserJSON(ctx, invocationsConfigPath, records); err != nil {
		return fmt.Errorf("write invocations: %w", err)
	}
	return nil
}

func (s *invocationStateStore) Delete(ctx context.Context, agentKey string) error {
	config, err := azdext.NewConfigHelper(s.client)
	if err != nil {
		return fmt.Errorf("create invocation config helper: %w", err)
	}
	var records map[string]savedInvocation
	found, err := config.GetUserJSON(ctx, invocationsConfigPath, &records)
	if err != nil {
		return fmt.Errorf("read invocations: %w", err)
	}
	if !found || records == nil {
		return nil
	}
	delete(records, agentKey)
	if err := config.SetUserJSON(ctx, invocationsConfigPath, records); err != nil {
		return fmt.Errorf("write invocations: %w", err)
	}
	return nil
}
