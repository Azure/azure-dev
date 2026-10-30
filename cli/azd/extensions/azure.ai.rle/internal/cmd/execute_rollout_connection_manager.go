// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	executeRolloutHandshakeMaxAttempts = 4
	executeRolloutRetryBaseDelay       = 250 * time.Millisecond
	executeRolloutRetryMaxDelay        = 4 * time.Second
	executeRolloutRetryAfterMax        = 30 * time.Second
	executeRolloutSubmissionMaxRetries = 3
	executeRolloutMaxQueuedSubmissions = 64
	executeRolloutMaxDrainingSockets   = 4
)

type executeRolloutSocketState string

const (
	executeRolloutSocketConnecting executeRolloutSocketState = "Connecting"
	executeRolloutSocketActive     executeRolloutSocketState = "Active"
	executeRolloutSocketDraining   executeRolloutSocketState = "Draining"
	executeRolloutSocketClosed     executeRolloutSocketState = "Closed"
)

type executeRolloutManagedSocket struct {
	sendMu      sync.Mutex
	connection  *websocket.Conn
	protocol    string
	state       executeRolloutSocketState
	submissions map[string]*executeRolloutSubmission
	readerDone  chan struct{}
}

type executeRolloutSubmission struct {
	request              executeRolloutRequest
	onProgress           func(executeRolloutProgress) error
	result               chan executeRolloutSubmissionResult
	origin               *executeRolloutManagedSocket
	lastProgressSequence int64
	retryCount           int
	done                 bool
}

type executeRolloutSubmissionResult struct {
	response *executeRolloutResponse
	err      error
}

type executeRolloutConnectionManager struct {
	ctx                context.Context
	cancel             context.CancelFunc
	client             *rleClient
	environmentName    string
	environmentVersion string
	loomBearerToken    string

	mu              sync.Mutex
	connectMu       sync.Mutex
	active          *executeRolloutManagedSocket
	sockets         map[*executeRolloutManagedSocket]struct{}
	submissions     map[string]*executeRolloutSubmission
	retryNotBefore  time.Time
	everConnected   bool
	closed          bool
	submissionSlots chan struct{}
}

type executeRolloutOutcomeUnknownError struct {
	rolloutIDs []string
	cause      error
}

func (e *executeRolloutOutcomeUnknownError) Error() string {
	return fmt.Sprintf(
		"Execute Rollout connection closed before terminal results; outcome unknown for rollout IDs %s: %v",
		strings.Join(e.rolloutIDs, ", "),
		e.cause,
	)
}

func (e *executeRolloutOutcomeUnknownError) Unwrap() error {
	return e.cause
}

var executeRolloutHandshakeRetryDelay = defaultExecuteRolloutHandshakeRetryDelay
var executeRolloutBeforeSend = func() {}

func newExecuteRolloutConnectionManager(
	ctx context.Context,
	client *rleClient,
	environmentName string,
	environmentVersion string,
	loomBearerToken string,
) *executeRolloutConnectionManager {
	managerCtx, cancel := context.WithCancel(ctx)
	return &executeRolloutConnectionManager{
		ctx:                managerCtx,
		cancel:             cancel,
		client:             client,
		environmentName:    environmentName,
		environmentVersion: environmentVersion,
		loomBearerToken:    loomBearerToken,
		sockets:            make(map[*executeRolloutManagedSocket]struct{}),
		submissions:        make(map[string]*executeRolloutSubmission),
		submissionSlots:    make(chan struct{}, executeRolloutMaxQueuedSubmissions),
	}
}

func (m *executeRolloutConnectionManager) Execute(
	ctx context.Context,
	request executeRolloutRequest,
	onProgress func(executeRolloutProgress) error,
) (*executeRolloutResponse, error) {
	select {
	case m.submissionSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return nil, fmt.Errorf(
			"Execute Rollout queue is full (maximum %d unresolved submissions)",
			executeRolloutMaxQueuedSubmissions,
		)
	}

	submission := &executeRolloutSubmission{
		request:    request,
		onProgress: onProgress,
		result:     make(chan executeRolloutSubmissionResult, 1),
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		<-m.submissionSlots
		return nil, errors.New("Execute Rollout connection manager is closed")
	}
	if _, exists := m.submissions[request.RolloutID]; exists {
		m.mu.Unlock()
		<-m.submissionSlots
		return nil, fmt.Errorf("rollout ID %q already has an unresolved submission", request.RolloutID)
	}
	m.submissions[request.RolloutID] = submission
	m.mu.Unlock()

	if err := m.dispatch(ctx, submission); err != nil {
		m.finishSubmission(submission, nil, err)
	}

	select {
	case result := <-submission.result:
		return result.response, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *executeRolloutConnectionManager) dispatch(
	ctx context.Context,
	submission *executeRolloutSubmission,
) error {
	frame, err := encodeExecuteRolloutFrame(
		executeRolloutFrameHeader{
			Type:      executeRolloutFrameExecute,
			RolloutID: submission.request.RolloutID,
		},
		submission.request,
	)
	if err != nil {
		return err
	}

	for {
		socket, err := m.activeSocket(ctx, submission.request.RolloutID)
		if err != nil {
			return err
		}

		socket.sendMu.Lock()
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			socket.sendMu.Unlock()
			return errors.New("Execute Rollout connection manager is closed")
		}
		if socket.state != executeRolloutSocketActive || m.active != socket {
			m.mu.Unlock()
			socket.sendMu.Unlock()
			continue
		}
		connection := socket.connection
		m.mu.Unlock()

		if err := connection.SetWriteDeadline(time.Now().Add(executeRolloutWriteTimeout)); err != nil {
			socket.sendMu.Unlock()
			m.failSocket(socket, fmt.Errorf("set Execute Rollout write deadline: %w", err))
			continue
		}

		executeRolloutBeforeSend()

		// From this point onward a failed write is ambiguous: some or all frame bytes may
		// have reached the service, so this rollout must never be submitted automatically.
		m.mu.Lock()
		if socket.state != executeRolloutSocketActive || m.active != socket {
			m.mu.Unlock()
			socket.sendMu.Unlock()
			continue
		}
		submission.origin = socket
		socket.submissions[submission.request.RolloutID] = submission
		m.mu.Unlock()

		err = connection.WriteMessage(websocket.BinaryMessage, frame)
		socket.sendMu.Unlock()
		if err != nil {
			m.failSocket(socket, fmt.Errorf("send Execute Rollout request: %w", err))
			return nil
		}
		return nil
	}
}

func (m *executeRolloutConnectionManager) activeSocket(
	ctx context.Context,
	routeRolloutID string,
) (*executeRolloutManagedSocket, error) {
	m.mu.Lock()
	if m.active != nil && m.active.state == executeRolloutSocketActive {
		socket := m.active
		m.mu.Unlock()
		return socket, nil
	}
	m.mu.Unlock()

	m.connectMu.Lock()
	defer m.connectMu.Unlock()

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errors.New("Execute Rollout connection manager is closed")
	}
	if m.active != nil && m.active.state == executeRolloutSocketActive {
		socket := m.active
		m.mu.Unlock()
		return socket, nil
	}
	if m.drainingSocketCountLocked() >= executeRolloutMaxDrainingSockets {
		m.mu.Unlock()
		return nil, fmt.Errorf(
			"Execute Rollout has %d draining connections; refusing another replacement until one closes",
			executeRolloutMaxDrainingSockets,
		)
	}
	retryNotBefore := m.retryNotBefore
	socket := &executeRolloutManagedSocket{
		state:       executeRolloutSocketConnecting,
		submissions: make(map[string]*executeRolloutSubmission),
		readerDone:  make(chan struct{}),
	}
	m.sockets[socket] = struct{}{}
	m.mu.Unlock()

	if err := waitUntil(ctx, retryNotBefore); err != nil {
		m.closeFailedConnection(socket)
		return nil, err
	}
	if err := m.connectSocket(ctx, socket, routeRolloutID); err != nil {
		m.closeFailedConnection(socket)
		m.mu.Lock()
		everConnected := m.everConnected
		m.mu.Unlock()
		if everConnected {
			// Do not expose a replacement failure as executeRolloutHandshakeError.
			// The outer command may fall back to HTTP only before any socket has been
			// used; after that, changing transports could replay an ambiguous rollout.
			return nil, errors.New("replace Execute Rollout connection: " + err.Error())
		}
		return nil, err
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		m.closeSocket(socket, websocket.CloseGoingAway, "RLE CLI connection manager closed.")
		return nil, errors.New("Execute Rollout connection manager is closed")
	}
	socket.state = executeRolloutSocketActive
	m.active = socket
	m.everConnected = true
	m.retryNotBefore = time.Time{}
	m.mu.Unlock()

	go m.readSocket(socket)
	return socket, nil
}

func (m *executeRolloutConnectionManager) connectSocket(
	ctx context.Context,
	socket *executeRolloutManagedSocket,
	routeRolloutID string,
) error {
	endpoint, err := m.client.executeRolloutWebSocketURL(
		m.environmentName,
		m.environmentVersion,
		routeRolloutID,
	)
	if err != nil {
		return err
	}

	var lastError error
	for attempt := range executeRolloutHandshakeMaxAttempts {
		authorization, err := m.client.authorizationHeader(ctx)
		if err != nil {
			return fmt.Errorf("authenticate to Foundry: %w", err)
		}
		headers := http.Header{}
		headers.Set("Authorization", authorization)
		headers.Set(executeRolloutHeader, m.loomBearerToken)

		connection, response, err := dialExecuteRolloutWebSocket(ctx, endpoint, headers)
		if err == nil {
			negotiated := connection.Subprotocol()
			if negotiated != executeRolloutSubprotocolV2 && negotiated != executeRolloutSubprotocolV1 {
				_ = connection.Close()
				return newExecuteRolloutHandshakeError(fmt.Errorf(
					"server did not select a supported Execute Rollout subprotocol (got %q)",
					negotiated,
				), nil)
			}
			connection.SetReadLimit(maxExecuteRolloutFrameBytes)
			socket.connection = connection
			socket.protocol = negotiated
			return nil
		}

		retryable := isRetryableExecuteRolloutHandshakeError(err)
		if response != nil {
			retryable = isRetryableExecuteRolloutHandshakeStatus(response.StatusCode)
		}
		if !retryable || attempt == executeRolloutHandshakeMaxAttempts-1 {
			return newExecuteRolloutHandshakeError(err, response)
		}
		delay, delayErr := executeRolloutHandshakeRetryDelay(attempt, response)
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if delayErr != nil {
			return fmt.Errorf("calculate Execute Rollout handshake retry delay: %w", delayErr)
		}
		lastError = err
		if err := waitFor(ctx, delay); err != nil {
			return err
		}
	}
	return newExecuteRolloutHandshakeError(lastError, nil)
}

func (m *executeRolloutConnectionManager) readSocket(socket *executeRolloutManagedSocket) {
	defer close(socket.readerDone)
	for {
		messageType, message, err := socket.connection.ReadMessage()
		if err != nil {
			m.failSocket(socket, fmt.Errorf("read Execute Rollout response: %w", err))
			return
		}
		if messageType != websocket.BinaryMessage {
			continue
		}
		header, payload, err := decodeExecuteRolloutFrame(message)
		if err != nil {
			m.failSocket(socket, err)
			return
		}
		if header.Type == executeRolloutFrameDraining {
			var notification executeRolloutDrainNotification
			if err := json.Unmarshal(payload, &notification); err != nil {
				m.failSocket(socket, fmt.Errorf("decode Execute Rollout drain notification: %w", err))
				return
			}
			if notification.DrainTimeoutSeconds < 0 || notification.RetryAfterSeconds < 0 {
				m.failSocket(socket, errors.New("Execute Rollout drain notification contains a negative duration"))
				return
			}
			m.markSocketDraining(socket, notification)
			continue
		}

		m.mu.Lock()
		submission := socket.submissions[header.RolloutID]
		m.mu.Unlock()
		if submission == nil {
			continue
		}

		switch header.Type {
		case executeRolloutFrameProgress:
			progress, err := decodeExecuteRolloutProgress(
				payload,
				header.RolloutID,
				submission.lastProgressSequence,
			)
			if err != nil {
				m.finishSubmission(submission, nil, err)
				continue
			}
			submission.lastProgressSequence = progress.Sequence
			if submission.onProgress != nil {
				if err := submission.onProgress(progress); err != nil {
					m.finishSubmission(
						submission,
						nil,
						fmt.Errorf("write Execute Rollout progress: %w", err),
					)
				}
			}
		case executeRolloutFrameCompleted:
			var result executeRolloutResponse
			if err := json.Unmarshal(payload, &result); err != nil {
				m.finishSubmission(submission, nil, fmt.Errorf("decode RLE response: %w", err))
				continue
			}
			if result.RolloutID != header.RolloutID {
				m.finishSubmission(
					submission,
					nil,
					errors.New("execute response rollout ID does not match the frame"),
				)
				continue
			}
			m.finishSubmission(submission, &result, nil)
		case executeRolloutFrameError:
			m.handleSubmissionError(socket, submission, payload)
		}
	}
}

func (m *executeRolloutConnectionManager) handleSubmissionError(
	socket *executeRolloutManagedSocket,
	submission *executeRolloutSubmission,
	payload []byte,
) {
	frameError := newExecuteRolloutFrameError(payload)
	code, retryAfter := executeRolloutErrorDetails(payload)
	if code != "ServerDraining" {
		// ServerShuttingDown and DuplicateRolloutId are intentionally terminal here.
		m.finishSubmission(submission, nil, frameError)
		return
	}

	m.mu.Lock()
	if submission.done || submission.origin != socket {
		m.mu.Unlock()
		return
	}
	delete(socket.submissions, submission.request.RolloutID)
	submission.origin = nil
	submission.lastProgressSequence = 0
	submission.retryCount++
	exhausted := submission.retryCount > executeRolloutSubmissionMaxRetries
	if socket.state == executeRolloutSocketActive {
		socket.state = executeRolloutSocketDraining
		if m.active == socket {
			m.active = nil
		}
	}
	if retryAfter > 0 {
		m.extendRetryNotBeforeLocked(retryAfter)
	}
	m.mu.Unlock()
	if exhausted {
		m.finishSubmission(submission, nil, frameError)
		return
	}

	go func() {
		if err := m.dispatch(m.ctx, submission); err != nil {
			m.finishSubmission(submission, nil, err)
		}
	}()
}

func (m *executeRolloutConnectionManager) markSocketDraining(
	socket *executeRolloutManagedSocket,
	notification executeRolloutDrainNotification,
) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if socket.state == executeRolloutSocketClosed || socket.state == executeRolloutSocketDraining {
		return
	}
	socket.state = executeRolloutSocketDraining
	if m.active == socket {
		m.active = nil
	}
	if notification.RetryAfterSeconds > 0 {
		m.extendRetryNotBeforeLocked(time.Duration(notification.RetryAfterSeconds) * time.Second)
	}
	// drain_timeout_seconds is advisory server cleanup timing. The client deliberately
	// does not close this socket at that deadline because terminal delivery may continue.
}

func (m *executeRolloutConnectionManager) failSocket(
	socket *executeRolloutManagedSocket,
	cause error,
) {
	m.mu.Lock()
	if socket.state == executeRolloutSocketClosed {
		m.mu.Unlock()
		return
	}
	socket.state = executeRolloutSocketClosed
	if m.active == socket {
		m.active = nil
	}
	delete(m.sockets, socket)

	rolloutIDs := make([]string, 0, len(socket.submissions))
	submissions := make([]*executeRolloutSubmission, 0, len(socket.submissions))
	for rolloutID, submission := range socket.submissions {
		if submission.done || submission.origin != socket {
			continue
		}
		rolloutIDs = append(rolloutIDs, rolloutID)
		submissions = append(submissions, submission)
	}
	slices.Sort(rolloutIDs)
	m.mu.Unlock()

	if socket.connection != nil {
		_ = socket.connection.Close()
	}
	if len(submissions) == 0 {
		return
	}
	outcomeErr := &executeRolloutOutcomeUnknownError{
		rolloutIDs: rolloutIDs,
		cause:      cause,
	}
	for _, submission := range submissions {
		m.finishSubmission(submission, nil, outcomeErr)
	}
}

func (m *executeRolloutConnectionManager) finishSubmission(
	submission *executeRolloutSubmission,
	response *executeRolloutResponse,
	err error,
) {
	m.mu.Lock()
	if submission.done {
		m.mu.Unlock()
		return
	}
	submission.done = true
	if submission.origin != nil {
		delete(submission.origin.submissions, submission.request.RolloutID)
		submission.origin = nil
	}
	delete(m.submissions, submission.request.RolloutID)
	m.mu.Unlock()

	<-m.submissionSlots
	submission.result <- executeRolloutSubmissionResult{response: response, err: err}
}

func (m *executeRolloutConnectionManager) Close() {
	m.cancel()

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	sockets := make([]*executeRolloutManagedSocket, 0, len(m.sockets))
	for socket := range m.sockets {
		sockets = append(sockets, socket)
	}
	type unresolvedSubmission struct {
		submission *executeRolloutSubmission
		sent       bool
	}
	submissions := make([]unresolvedSubmission, 0, len(m.submissions))
	for _, submission := range m.submissions {
		submissions = append(submissions, unresolvedSubmission{
			submission: submission,
			sent:       submission.origin != nil,
		})
	}
	m.mu.Unlock()

	for _, unresolved := range submissions {
		err := errors.New("Execute Rollout connection manager closed before the rollout completed")
		if unresolved.sent {
			err = &executeRolloutOutcomeUnknownError{
				rolloutIDs: []string{unresolved.submission.request.RolloutID},
				cause:      err,
			}
		}
		m.finishSubmission(unresolved.submission, nil, err)
	}
	for _, socket := range sockets {
		m.closeSocket(socket, websocket.CloseNormalClosure, "RLE CLI finished collecting rollout results.")
	}
}

func (m *executeRolloutConnectionManager) closeSocket(
	socket *executeRolloutManagedSocket,
	code int,
	reason string,
) {
	m.mu.Lock()
	if socket.state == executeRolloutSocketClosed {
		m.mu.Unlock()
		return
	}
	connection := socket.connection
	m.mu.Unlock()
	if connection == nil {
		m.closeFailedConnection(socket)
		return
	}

	socket.sendMu.Lock()
	deadline := time.Now().Add(executeRolloutCloseTimeout)
	_ = connection.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(code, reason),
		deadline,
	)
	socket.sendMu.Unlock()

	select {
	case <-socket.readerDone:
	case <-time.After(executeRolloutCloseTimeout):
	}
	_ = connection.Close()
	m.closeFailedConnection(socket)
}

func (m *executeRolloutConnectionManager) closeFailedConnection(socket *executeRolloutManagedSocket) {
	m.mu.Lock()
	socket.state = executeRolloutSocketClosed
	if m.active == socket {
		m.active = nil
	}
	delete(m.sockets, socket)
	m.mu.Unlock()
}

func (m *executeRolloutConnectionManager) drainingSocketCountLocked() int {
	count := 0
	for socket := range m.sockets {
		if socket.state == executeRolloutSocketDraining {
			count++
		}
	}
	return count
}

func (m *executeRolloutConnectionManager) extendRetryNotBeforeLocked(delay time.Duration) {
	delay = min(delay, executeRolloutRetryAfterMax)
	notBefore := time.Now().Add(delay)
	if notBefore.After(m.retryNotBefore) {
		m.retryNotBefore = notBefore
	}
}

func defaultExecuteRolloutHandshakeRetryDelay(
	attempt int,
	response *http.Response,
) (time.Duration, error) {
	ceiling := min(executeRolloutRetryBaseDelay<<attempt, executeRolloutRetryMaxDelay)
	delay, err := rand.Int(rand.Reader, big.NewInt(int64(ceiling)+1))
	if err != nil {
		return 0, err
	}
	result := time.Duration(delay.Int64())
	if retryAfter := executeRolloutRetryAfter(response, time.Now()); retryAfter > result {
		result = retryAfter
	}
	return min(result, executeRolloutRetryAfterMax), nil
}

func executeRolloutRetryAfter(response *http.Response, now time.Time) time.Duration {
	if response == nil {
		return 0
	}
	value := strings.TrimSpace(response.Header.Get("Retry-After"))
	if value == "" {
		return 0
	}
	if retryAt, err := http.ParseTime(value); err == nil {
		return max(0, retryAt.Sub(now))
	}
	seconds, err := time.ParseDuration(value + "s")
	if err != nil {
		return 0
	}
	return max(0, seconds)
}

func executeRolloutErrorDetails(payload []byte) (string, time.Duration) {
	type errorBody struct {
		Code              string     `json:"code,omitempty"`
		RetryAfterSeconds int        `json:"retry_after_seconds,omitempty"`
		Error             *errorBody `json:"error,omitempty"`
	}
	var body errorBody
	if err := json.Unmarshal(payload, &body); err != nil {
		return "", 0
	}
	current := &body
	for current.Error != nil {
		current = current.Error
	}
	return strings.TrimSpace(current.Code), time.Duration(max(0, current.RetryAfterSeconds)) * time.Second
}

func isRetryableExecuteRolloutHandshakeError(err error) bool {
	_, ok := errors.AsType[net.Error](err)
	return ok
}

func isRetryableExecuteRolloutHandshakeStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusRequestTimeout,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func waitUntil(ctx context.Context, notBefore time.Time) error {
	if notBefore.IsZero() {
		return nil
	}
	return waitFor(ctx, time.Until(notBefore))
}

func waitFor(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
