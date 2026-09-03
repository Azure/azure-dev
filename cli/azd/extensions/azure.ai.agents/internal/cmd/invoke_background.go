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
	"os"
	"strconv"
	"sync"
	"time"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

const (
	backgroundCursorPersistInterval   = 3 * time.Second
	backgroundCursorPersistTimeout    = 30 * time.Second
	backgroundCursorPersistEventCount = 64
)

var (
	errBackgroundProgressPersisterClosed = errors.New("background progress persister is closed")
	errBackgroundNoWait                  = errors.New("background Response identity saved")
)

type backgroundPersistTimer interface {
	Stop() bool
}

type backgroundPersistTimerFactory func(time.Duration, func()) backgroundPersistTimer

const maxConsecutiveReconnectFailures = 5

func isTerminalResponseStatus(status string) bool {
	switch status {
	case "completed", "failed", "incomplete", "cancelled":
		return true
	default:
		return false
	}
}

// isResponseLifecycleEvent reports whether an event marks a Response being created,
// queued, started, or entering a terminal state.
func isResponseLifecycleEvent(eventType string) bool {
	switch eventType {
	case "response.created", "response.queued", "response.in_progress",
		"response.completed", "response.failed", "response.incomplete", "response.cancelled":
		return true
	// Output deltas use throttled persistence. Unknown future events remain
	// non-lifecycle until explicitly supported.
	default:
		return false
	}
}

type backgroundProgressPersister struct {
	mu                 sync.Mutex
	store              responseStateStore
	agentKey           string
	sessionID          string
	conversationID     string
	writer             io.Writer
	now                func() time.Time
	latest             savedBackgroundResponse
	persistedResponse  string
	persistedStatus    string
	lastPersistedAt    time.Time
	eventsSincePersist int
	printedResponseID  bool
	dirty              bool
	timerFactory       backgroundPersistTimerFactory
	timerContext       func(context.Context) (context.Context, context.CancelFunc)
	timer              backgroundPersistTimer
	timerGeneration    uint64
	pendingTimerErr    error
	closed             bool
}

func newBackgroundProgressPersister(
	store responseStateStore,
	agentKey string,
	sessionID string,
	conversationID string,
	writer io.Writer,
) *backgroundProgressPersister {
	return &backgroundProgressPersister{
		store:          store,
		agentKey:       agentKey,
		sessionID:      sessionID,
		conversationID: conversationID,
		writer:         writer,
		now:            time.Now,
		timerFactory: func(delay time.Duration, callback func()) backgroundPersistTimer {
			return time.AfterFunc(delay, callback)
		},
		timerContext: func(ctx context.Context) (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.WithoutCancel(ctx), backgroundCursorPersistTimeout)
		},
	}
}

func (p *backgroundProgressPersister) Resume(record savedBackgroundResponse) {
	p.latest = record
	p.persistedResponse = record.ResponseID
	p.persistedStatus = record.Status
	p.lastPersistedAt = p.now()
	p.printedResponseID = true
	p.dirty = false
}

func (p *backgroundProgressPersister) Apply(ctx context.Context, progress responsesStreamProgress) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return errBackgroundProgressPersisterClosed
	}
	if err := p.takeTimerErrorLocked(); err != nil {
		return err
	}
	if progress.ResponseID == "" {
		return nil
	}

	p.latest = savedBackgroundResponse{
		ResponseID:         progress.ResponseID,
		LastSequenceNumber: progress.Cursor,
		Status:             progress.Status,
		SessionID:          p.sessionID,
		ConversationID:     p.conversationID,
	}
	p.dirty = true
	p.eventsSincePersist++
	now := p.now()
	shouldPersist := p.persistedResponse == "" ||
		isResponseLifecycleEvent(progress.EventType) ||
		progress.Status != p.persistedStatus ||
		progress.Terminal ||
		p.eventsSincePersist >= backgroundCursorPersistEventCount ||
		(!p.lastPersistedAt.IsZero() && now.Sub(p.lastPersistedAt) >= backgroundCursorPersistInterval)
	if !shouldPersist {
		p.scheduleTimerLocked(ctx, now)
		return nil
	}
	return p.persistLocked(ctx, now)
}

func (p *backgroundProgressPersister) Flush(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return errBackgroundProgressPersisterClosed
	}
	p.stopTimerLocked()
	if err := p.takeTimerErrorLocked(); err != nil {
		return err
	}
	if !p.dirty {
		return nil
	}
	return p.persistLocked(ctx, p.now())
}

// Close stops asynchronous persistence and reports any timer error not already
// observed by Apply or Flush. It intentionally does not flush dirty state when
// command cancellation caused the stream to exit.
func (p *backgroundProgressPersister) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.stopTimerLocked()
	p.closed = true
	return p.takeTimerErrorLocked()
}

func (p *backgroundProgressPersister) scheduleTimerLocked(ctx context.Context, now time.Time) {
	if p.timer != nil || !p.dirty {
		return
	}

	delay := backgroundCursorPersistInterval
	if !p.lastPersistedAt.IsZero() {
		delay = max(backgroundCursorPersistInterval-now.Sub(p.lastPersistedAt), 0)
	}
	p.timerGeneration++
	generation := p.timerGeneration
	p.timer = p.timerFactory(delay, func() {
		p.persistFromTimer(ctx, generation)
	})
}

func (p *backgroundProgressPersister) persistFromTimer(ctx context.Context, generation uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed || generation != p.timerGeneration {
		return
	}
	p.timer = nil
	if !p.dirty || p.pendingTimerErr != nil {
		return
	}
	persistCtx, cancel := p.timerContext(ctx)
	defer cancel()
	if err := p.persistLocked(persistCtx, p.now()); err != nil {
		p.pendingTimerErr = err
	}
}

func (p *backgroundProgressPersister) stopTimerLocked() {
	if p.timer == nil {
		return
	}
	p.timerGeneration++
	p.timer.Stop()
	p.timer = nil
}

func (p *backgroundProgressPersister) takeTimerErrorLocked() error {
	err := p.pendingTimerErr
	p.pendingTimerErr = nil
	return err
}

func (p *backgroundProgressPersister) persistLocked(ctx context.Context, now time.Time) error {
	p.stopTimerLocked()
	if err := p.store.Save(ctx, p.agentKey, p.latest); err != nil {
		if !p.printedResponseID {
			_, _ = fmt.Fprintf(
				p.writer,
				"Response:     %s\nWARNING: This background Response was accepted, but its state was not saved. "+
					"Save the Response ID before retrying.\n",
				p.latest.ResponseID,
			)
			p.printedResponseID = true
		}
		return err
	}
	if !p.printedResponseID {
		if _, err := fmt.Fprintf(p.writer, "Response:     %s\n", p.latest.ResponseID); err != nil {
			return err
		}
		p.printedResponseID = true
	}
	p.persistedResponse = p.latest.ResponseID
	p.persistedStatus = p.latest.Status
	p.lastPersistedAt = now
	p.eventsSincePersist = 0
	p.dirty = false
	return nil
}

func printResponseStatus(writer io.Writer, status string) error {
	_, err := fmt.Fprintf(writer, "Status: %s\n", status)
	return err
}

// responsesResumeRemote resolves the saved Response context and resumes following its output.
func (a *InvokeAction) responsesResumeRemote(ctx context.Context) error {
	rc, store, record, err := a.resolveSavedBackgroundResponse(ctx)
	if err != nil {
		return err
	}
	defer rc.azdClient.Close()

	fmt.Printf("Agent:        %s (remote)\n", rc.name)
	fmt.Printf("Response:     %s\n", record.ResponseID)
	fmt.Printf("Session:      %s\n", record.SessionID)
	fmt.Printf("Conversation: %s\n\n", record.ConversationID)
	return classifyResponseLifecycleHTTPError(
		a.followBackgroundResponse(ctx, rc, store, *record, os.Stdout),
		exterrors.OpResumeBackgroundResponse,
	)
}

// responseLifecycleHTTPError preserves HTTP response metadata for classification at the operation boundary.
type responseLifecycleHTTPError struct {
	method     string
	requestURL string
	statusCode int
	status     string
	body       []byte
}

func (e *responseLifecycleHTTPError) Error() string {
	return fmt.Sprintf(
		"%s %s failed with HTTP %d: %s\n%s",
		e.method,
		e.requestURL,
		e.statusCode,
		e.status,
		e.body,
	)
}

func classifyResponseLifecycleHTTPError(cause error, operation string) error {
	if cause == nil {
		return nil
	}
	httpErr, ok := errors.AsType[*responseLifecycleHTTPError](cause)
	if !ok {
		return cause
	}

	serviceName := ""
	if parsedURL, err := url.Parse(httpErr.requestURL); err == nil {
		serviceName = parsedURL.Hostname()
	}
	operationLabel := "background Response request"
	switch operation {
	case exterrors.OpResumeBackgroundResponse:
		operationLabel = "resuming background Response"
	case exterrors.OpCancelBackgroundResponse:
		operationLabel = "cancelling background Response"
	}
	serviceErr := exterrors.Service(
		operation,
		strconv.Itoa(httpErr.statusCode),
		fmt.Sprintf("%s failed with HTTP %d: %s", operationLabel, httpErr.statusCode, httpErr.status),
		serviceName,
		"",
	)
	serviceErr.StatusCode = httpErr.statusCode
	return serviceErr
}

// backgroundFollowAttempt describes whether one follow attempt completed or should be retried.
type backgroundFollowAttempt struct {
	completed        bool
	acceptedProgress bool
	record           savedBackgroundResponse
	retryCause       error
	retryAfter       time.Duration
}

// backgroundFollowRequestResult separates an opened stream from a retryable setup failure.
type backgroundFollowRequestResult struct {
	response   *http.Response
	retryCause error
	retryAfter time.Duration
}

// followBackgroundResponse follows a Response through transient disconnects until it reaches a terminal state.
func (a *InvokeAction) followBackgroundResponse(
	ctx context.Context,
	rc *remoteContext,
	store responseStateStore,
	record savedBackgroundResponse,
	writer io.Writer,
) (returnErr error) {
	if isTerminalResponseStatus(record.Status) {
		return printResponseStatus(writer, record.Status)
	}

	progressPersister := newBackgroundProgressPersister(
		store,
		rc.agentKey,
		record.SessionID,
		record.ConversationID,
		writer,
	)
	progressPersister.Resume(record)
	defer func() {
		returnErr = errors.Join(returnErr, progressPersister.Close())
	}()

	consecutiveFailures := 0
	for {
		attempt, err := a.followBackgroundResponseOnce(ctx, rc, store, record, progressPersister, writer)
		if err != nil {
			return err
		}
		if attempt.completed {
			return nil
		}

		record = attempt.record
		consecutiveFailures = nextReconnectFailureCount(consecutiveFailures, attempt.acceptedProgress)
		if consecutiveFailures >= maxConsecutiveReconnectFailures {
			return a.finalizeReconnectFailure(ctx, rc, store, record, attempt.retryCause, writer)
		}
		if err := sleepWithContext(
			ctx,
			reconnectRetryDelay(attempt.retryAfter, consecutiveFailures-1),
		); err != nil {
			return err
		}
	}
}

// followBackgroundResponseOnce opens and consumes one stream, returning retry state when the stream disconnects.
func (a *InvokeAction) followBackgroundResponseOnce(
	ctx context.Context,
	rc *remoteContext,
	store responseStateStore,
	record savedBackgroundResponse,
	progressPersister *backgroundProgressPersister,
	writer io.Writer,
) (backgroundFollowAttempt, error) {
	requestResult, err := a.requestBackgroundResponseFollow(ctx, rc, record)
	if err != nil {
		return backgroundFollowAttempt{}, err
	}
	if requestResult.retryCause != nil {
		return backgroundFollowAttempt{
			record:     record,
			retryCause: requestResult.retryCause,
			retryAfter: requestResult.retryAfter,
		}, nil
	}
	return a.consumeBackgroundResponseFollow(
		ctx,
		rc,
		store,
		record,
		progressPersister,
		requestResult.response,
		writer,
	)
}

// requestBackgroundResponseFollow acquires a fresh token and opens the stream after the last persisted cursor.
func (a *InvokeAction) requestBackgroundResponseFollow(
	ctx context.Context,
	rc *remoteContext,
	record savedBackgroundResponse,
) (backgroundFollowRequestResult, error) {
	token, err := a.acquireBearerToken(ctx)
	if err != nil {
		return backgroundFollowRequestResult{}, err
	}
	rc.bearerToken = token

	followURL := buildResponseLifecycleURL(
		rc.projectEndpoint,
		rc.name,
		record.ResponseID,
		rc.apiVersion,
		true,
		record.LastSequenceNumber,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, followURL, nil)
	if err != nil {
		return backgroundFollowRequestResult{}, fmt.Errorf("create Response follow request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+rc.bearerToken)
	req.Header.Set("Accept", "text/event-stream")
	applyCustomHeaders(req, a.clientHeaders)
	applyRemoteUserIdentityHeader(req, &a.flags.userIdentityFlags)

	//nolint:gosec // G704: project endpoint is validated as a Foundry URL; response ID is path-escaped.
	resp, requestErr := backgroundHTTPClient().Do(req)
	if requestErr != nil {
		return backgroundFollowRequestResult{
			retryCause: fmt.Errorf("follow background Response %s: %w", record.ResponseID, requestErr),
		}, nil
	}
	return classifyBackgroundFollowResponse(resp, followURL)
}

// classifyBackgroundFollowResponse distinguishes an opened stream from retryable and terminal HTTP failures.
func classifyBackgroundFollowResponse(
	resp *http.Response,
	followURL string,
) (backgroundFollowRequestResult, error) {
	if resp.StatusCode < 400 {
		return backgroundFollowRequestResult{response: resp}, nil
	}

	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	followErr := &responseLifecycleHTTPError{
		method:     http.MethodGet,
		requestURL: followURL,
		statusCode: resp.StatusCode,
		status:     resp.Status,
		body:       body,
	}
	if !isRetryableResponseStatus(resp.StatusCode) {
		return backgroundFollowRequestResult{}, followErr
	}
	return backgroundFollowRequestResult{
		retryCause: followErr,
		retryAfter: httputil.RetryAfter(resp),
	}, nil
}

// consumeBackgroundResponseFollow renders and persists one stream, then classifies whether following is complete.
func (a *InvokeAction) consumeBackgroundResponseFollow(
	ctx context.Context,
	rc *remoteContext,
	store responseStateStore,
	record savedBackgroundResponse,
	progressPersister *backgroundProgressPersister,
	resp *http.Response,
	writer io.Writer,
) (backgroundFollowAttempt, error) {
	acceptedProgress := false
	onProgress := func(progress responsesStreamProgress) error {
		acceptedProgress = true
		if progress.ResponseID == "" {
			progress.ResponseID = record.ResponseID
		}
		if progress.Status == "" {
			progress.Status = record.Status
		}
		return progressPersister.Apply(ctx, progress)
	}
	streamErr := readResponsesSSE(
		ctx,
		resp.Body,
		writer,
		rc.name,
		responsesSSEOptions{
			requireTerminal: true,
			initialState: &responsesStreamInitialState{
				ResponseID: record.ResponseID,
				Cursor:     record.LastSequenceNumber,
				Status:     record.Status,
			},
			onProgress: onProgress,
		},
	)
	_ = resp.Body.Close()

	var flushErr error
	if ctx.Err() == nil {
		flushErr = progressPersister.Flush(ctx)
	}
	if progressPersister.latest.ResponseID != "" {
		record = progressPersister.latest
	}
	if streamErr == nil && flushErr == nil {
		return backgroundFollowAttempt{completed: true, record: record}, nil
	}
	if flushErr != nil {
		return backgroundFollowAttempt{}, errors.Join(streamErr, flushErr)
	}
	if errors.Is(streamErr, errResponsesStreamEndedBeforeIdentity) {
		return a.handleEmptyBackgroundFollow(ctx, rc, store, record, writer)
	}
	if !isRetryableBackgroundStreamError(streamErr) {
		return backgroundFollowAttempt{}, streamErr
	}

	latest, loadErr := store.Get(ctx, rc.agentKey)
	if loadErr != nil {
		return backgroundFollowAttempt{}, errors.Join(streamErr, classifyBackgroundResponseStateReadError(loadErr))
	}
	if latest != nil {
		record = *latest
	}
	if isTerminalResponseStatus(record.Status) {
		return backgroundFollowAttempt{completed: true, record: record},
			printResponseStatus(writer, record.Status)
	}
	return backgroundFollowAttempt{
		acceptedProgress: acceptedProgress,
		record:           record,
		retryCause:       streamErr,
	}, nil
}

// handleEmptyBackgroundFollow uses a snapshot GET to resolve a follow stream that returned no events.
func (a *InvokeAction) handleEmptyBackgroundFollow(
	ctx context.Context,
	rc *remoteContext,
	store responseStateStore,
	record savedBackgroundResponse,
	writer io.Writer,
) (backgroundFollowAttempt, error) {
	if err := a.ensureBearerToken(ctx, rc); err != nil {
		return backgroundFollowAttempt{}, err
	}
	updated, result, err := a.refreshResponseSnapshot(ctx, rc, store, record, rc.bearerToken)
	if err != nil {
		return backgroundFollowAttempt{}, errors.Join(
			fmt.Errorf("Response %s returned no new stream events", record.ResponseID),
			err,
		)
	}
	if !isTerminalResponseStatus(updated.Status) {
		return backgroundFollowAttempt{}, fmt.Errorf(
			"Response %s is still %s, but no new stream events were available; "+
				"try `azd ai agent invoke --resume` again",
			updated.ResponseID,
			updated.Status,
		)
	}
	if err := renderResponseSnapshot(writer, rc.name, result); err != nil {
		return backgroundFollowAttempt{}, err
	}
	if err := printResponseStatus(writer, updated.Status); err != nil {
		return backgroundFollowAttempt{}, err
	}
	return backgroundFollowAttempt{completed: true, record: updated}, nil
}

func (a *InvokeAction) responsesCancelRemote(ctx context.Context) error {
	rc, store, saved, err := a.resolveSavedBackgroundResponse(ctx)
	if err != nil {
		return err
	}
	defer rc.azdClient.Close()
	record := *saved

	if isTerminalResponseStatus(record.Status) {
		fmt.Printf("Response %s is already %s; nothing to cancel.\n", record.ResponseID, record.Status)
		return nil
	}

	rc.bearerToken, err = a.acquireBearerToken(ctx)
	if err != nil {
		return err
	}
	cancelURL := buildResponseCancelURL(rc.projectEndpoint, rc.name, record.ResponseID, rc.apiVersion)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cancelURL, nil)
	if err != nil {
		return fmt.Errorf("create Response cancel request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+rc.bearerToken)
	applyCustomHeaders(req, a.clientHeaders)
	applyRemoteUserIdentityHeader(req, &a.flags.userIdentityFlags)

	//nolint:gosec // G704: project endpoint is validated as a Foundry URL; response ID is path-escaped.
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("cancel background Response %s: %w", record.ResponseID, err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return fmt.Errorf("read cancel response: %w", readErr)
	}
	if resp.StatusCode >= 400 {
		cancelErr := &responseLifecycleHTTPError{
			method:     http.MethodPost,
			requestURL: cancelURL,
			statusCode: resp.StatusCode,
			status:     resp.Status,
			body:       body,
		}
		return classifyResponseLifecycleHTTPError(
			a.handleRejectedResponseCancel(ctx, rc, store, record, cancelErr, os.Stdout),
			exterrors.OpCancelBackgroundResponse,
		)
	}

	snapshot, decodeErr := decodeResponseSnapshot(body)
	if decodeErr == nil && snapshot.Status != "" {
		record.Status = snapshot.Status
	} else {
		record.Status = "cancelled"
	}
	if err := store.Save(ctx, rc.agentKey, record); err != nil {
		return fmt.Errorf("save cancelled Response state: %w", err)
	}
	fmt.Printf("Response %s is %s.\n", record.ResponseID, record.Status)
	return nil
}

func (a *InvokeAction) handleRejectedResponseCancel(
	ctx context.Context,
	rc *remoteContext,
	store responseStateStore,
	record savedBackgroundResponse,
	cancelErr error,
	writer io.Writer,
) error {
	updated, _, refreshErr := a.refreshResponseSnapshot(
		ctx,
		rc,
		store,
		record,
		rc.bearerToken,
	)
	if refreshErr != nil || !isTerminalResponseStatus(updated.Status) {
		return cancelErr
	}
	_, err := fmt.Fprintf(
		writer,
		"Response %s is already %s; nothing to cancel.\n",
		updated.ResponseID,
		updated.Status,
	)
	return err
}

func (a *InvokeAction) resolveSavedBackgroundResponse(
	ctx context.Context,
) (*remoteContext, responseStateStore, *savedBackgroundResponse, error) {
	rc, err := a.resolveRemoteContext(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	if rc.azdClient == nil {
		return nil, nil, nil, responseStateUnavailable(nil)
	}
	store := newUserConfigResponseStateStore(rc.azdClient)
	record, err := store.Get(ctx, rc.agentKey)
	if err != nil {
		rc.azdClient.Close()
		return nil, nil, nil, classifyBackgroundResponseStateReadError(err)
	}
	if record == nil || record.ResponseID == "" {
		rc.azdClient.Close()
		return nil, nil, nil, fmt.Errorf(
			"no saved background Response found; start one with `azd ai agent invoke --resumable \"<message>\"`",
		)
	}
	return rc, store, record, nil
}

func (a *InvokeAction) refreshResponseSnapshot(
	ctx context.Context,
	rc *remoteContext,
	store responseStateStore,
	record savedBackgroundResponse,
	token string,
) (savedBackgroundResponse, responseSnapshotResult, error) {
	snapshotURL := buildResponseLifecycleURL(
		rc.projectEndpoint,
		rc.name,
		record.ResponseID,
		rc.apiVersion,
		false,
		nil,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, snapshotURL, nil)
	if err != nil {
		return savedBackgroundResponse{}, responseSnapshotResult{},
			fmt.Errorf("create Response snapshot request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	applyCustomHeaders(req, a.clientHeaders)
	applyRemoteUserIdentityHeader(req, &a.flags.userIdentityFlags)
	//nolint:gosec // G704: project endpoint is validated as a Foundry URL; response ID is path-escaped.
	resp, err := backgroundHTTPClient().Do(req)
	if err != nil {
		return savedBackgroundResponse{}, responseSnapshotResult{},
			fmt.Errorf("retrieve Response snapshot: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return savedBackgroundResponse{}, responseSnapshotResult{},
			fmt.Errorf("read Response snapshot: %w", err)
	}
	if resp.StatusCode >= 400 {
		return savedBackgroundResponse{}, responseSnapshotResult{}, &responseLifecycleHTTPError{
			method:     http.MethodGet,
			requestURL: snapshotURL,
			statusCode: resp.StatusCode,
			status:     resp.Status,
			body:       body,
		}
	}
	snapshot, err := decodeResponseSnapshot(body)
	if err != nil {
		return savedBackgroundResponse{}, responseSnapshotResult{},
			fmt.Errorf("decode Response snapshot: %w", err)
	}
	if snapshot.ID != "" && snapshot.ID != record.ResponseID {
		return savedBackgroundResponse{}, responseSnapshotResult{},
			fmt.Errorf("Response snapshot ID %q does not match saved ID %q", snapshot.ID, record.ResponseID)
	}

	updated := record
	if snapshot.Status != "" {
		updated.Status = snapshot.Status
	}
	if err := store.Save(ctx, rc.agentKey, updated); err != nil {
		return savedBackgroundResponse{}, responseSnapshotResult{}, err
	}
	return updated, responseSnapshotResult{snapshot: snapshot, raw: body}, nil
}

func backgroundHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	return &http.Client{Transport: transport}
}

// finalizeReconnectFailure checks one final snapshot after reconnect retries are exhausted.
func (a *InvokeAction) finalizeReconnectFailure(
	ctx context.Context,
	rc *remoteContext,
	store responseStateStore,
	record savedBackgroundResponse,
	reconnectErr error,
	writer io.Writer,
) error {
	if err := a.ensureBearerToken(ctx, rc); err != nil {
		return errors.Join(reconnectErr, err)
	}
	updated, result, err := a.refreshResponseSnapshot(ctx, rc, store, record, rc.bearerToken)
	if err != nil {
		return errors.Join(reconnectErr, err)
	}
	if err := renderResponseSnapshot(writer, rc.name, result); err != nil {
		return errors.Join(reconnectErr, err)
	}
	if isTerminalResponseStatus(updated.Status) {
		return printResponseStatus(writer, updated.Status)
	}
	return fmt.Errorf(
		"%w: Response %s is still %s; try `azd ai agent invoke --resume` again",
		reconnectErr,
		updated.ResponseID,
		updated.Status,
	)
}

func isRetryableBackgroundStreamError(err error) bool {
	return errors.Is(err, errResponsesStreamDisconnected)
}

func isRetryableResponseStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}

func nextReconnectFailureCount(current int, acceptedProgress bool) int {
	if acceptedProgress {
		return 1
	}
	return current + 1
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

func decodeResponseSnapshot(body []byte) (responsesSnapshot, error) {
	var envelope struct {
		Response json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && len(envelope.Response) > 0 {
		body = envelope.Response
	}
	var snapshot responsesSnapshot
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&snapshot); err != nil {
		return responsesSnapshot{}, err
	}
	return snapshot, nil
}
