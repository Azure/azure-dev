// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
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
				"%w; rerun `azd ai agent responses follow --response-id %s` to replay and follow again",
				err,
				responseID,
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
