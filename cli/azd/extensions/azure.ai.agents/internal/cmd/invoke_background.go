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

	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

const maxConsecutiveReconnectFailures = 5

var errBackgroundNoWait = errors.New("background Response identity saved")

// responseProgressTracker saves only the current Response identity. Sequence
// progress remains process-local and is used solely to reconnect the running command.
type responseProgressTracker struct {
	store      responseStateStore
	agentKey   string
	writer     io.Writer
	responseID string
	cursor     *int64
	status     string
	saveErr    error
	printedID  bool
}

func (t *responseProgressTracker) Apply(ctx context.Context, progress responsesStreamProgress) error {
	if progress.Cursor != nil {
		t.cursor = new(*progress.Cursor)
	}
	if progress.Status != "" {
		t.status = progress.Status
	}
	if progress.ResponseID == "" || progress.ResponseID == t.responseID {
		return nil
	}
	if t.responseID != "" {
		return fmt.Errorf("Responses stream changed response ID from %q to %q", t.responseID, progress.ResponseID)
	}

	t.responseID = progress.ResponseID
	if t.store != nil && t.agentKey != "" {
		if err := t.store.Save(ctx, t.agentKey, savedResponse{ResponseID: progress.ResponseID}); err != nil {
			_, _ = fmt.Fprintf(
				t.writer,
				"Response:     %s\nWARNING: The Response was accepted, but its ID was not saved: %v\n",
				progress.ResponseID,
				err,
			)
			t.printedID = true
			t.saveErr = fmt.Errorf("save current Response: %w", err)
			return nil
		}
	}
	if !t.printedID {
		if _, err := fmt.Fprintf(t.writer, "Response:     %s\n", progress.ResponseID); err != nil {
			return err
		}
		t.printedID = true
	}
	return nil
}

// followResponse replays and follows a Response. The first request starts from
// the supplied cursor, which is nil for `responses follow` and may be non-nil
// when an attached background create reconnects. Later cursors are in-memory only.
func (a *InvokeAction) followResponse(
	ctx context.Context,
	rc *remoteContext,
	responseID string,
	cursor *int64,
	writer io.Writer,
) error {
	consecutiveFailures := 0
	status := ""

	for {
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
			cursor,
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
		resp, requestErr := responseStreamHTTPClient().Do(req)
		if requestErr != nil {
			consecutiveFailures++
			if consecutiveFailures >= maxConsecutiveReconnectFailures {
				return fmt.Errorf(
					"follow Response %s after %d attempts: %w; inspect it with "+
						"`azd ai agent responses show --response-id %s` or retry with "+
						"`azd ai agent responses follow --response-id %s`",
					responseID,
					consecutiveFailures,
					requestErr,
					responseID,
					responseID,
				)
			}
			if err := sleepWithContext(ctx, reconnectDelay(consecutiveFailures-1)); err != nil {
				return err
			}
			continue
		}

		if resp.StatusCode >= http.StatusBadRequest {
			retryAfter := httputil.RetryAfter(resp)
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			httpErr := &responseLifecycleHTTPError{
				method:     http.MethodGet,
				requestURL: followURL,
				statusCode: resp.StatusCode,
				status:     resp.Status,
				body:       body,
			}
			if !isRetryableResponseStatus(resp.StatusCode) {
				return httpErr
			}
			consecutiveFailures++
			if consecutiveFailures >= maxConsecutiveReconnectFailures {
				return followRetryError(httpErr, responseID)
			}
			if err := sleepWithContext(ctx, reconnectRetryDelay(retryAfter, consecutiveFailures-1)); err != nil {
				return err
			}
			continue
		}

		acceptedProgress := false
		streamErr := readResponsesSSE(
			ctx,
			resp.Body,
			writer,
			rc.name,
			responsesSSEOptions{
				requireTerminal: true,
				initialState: &responsesStreamInitialState{
					ResponseID: responseID,
					Cursor:     cursor,
					Status:     status,
				},
				onProgress: func(progress responsesStreamProgress) error {
					acceptedProgress = true
					if progress.Cursor != nil {
						cursor = new(*progress.Cursor)
					}
					if progress.Status != "" {
						status = progress.Status
					}
					return nil
				},
			},
		)
		_ = resp.Body.Close()
		if streamErr == nil {
			return nil
		}
		if !errors.Is(streamErr, errResponsesStreamDisconnected) || ctx.Err() != nil {
			return streamErr
		}
		if acceptedProgress {
			consecutiveFailures = 0
		}
		consecutiveFailures++
		if consecutiveFailures >= maxConsecutiveReconnectFailures {
			return followRetryError(streamErr, responseID)
		}
		if err := sleepWithContext(ctx, reconnectDelay(consecutiveFailures-1)); err != nil {
			return err
		}
	}
}

func followRetryError(cause error, responseID string) error {
	return fmt.Errorf(
		"%w; inspect it with `azd ai agent responses show --response-id %s` or retry with "+
			"`azd ai agent responses follow --response-id %s`",
		cause,
		responseID,
		responseID,
	)
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

func isRetryableResponseStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}

func reconnectDelay(attempt int) time.Duration {
	return min(time.Second<<attempt, 30*time.Second)
}

func reconnectRetryDelay(retryAfter time.Duration, attempt int) time.Duration {
	if retryAfter > 0 {
		return min(retryAfter, 30*time.Second)
	}
	return reconnectDelay(attempt)
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
