// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestDecodeExecuteRolloutDrainFrameWithoutRolloutID(t *testing.T) {
	frame, err := encodeExecuteRolloutFrame(
		executeRolloutFrameHeader{Type: executeRolloutFrameDraining},
		executeRolloutDrainNotification{
			DrainTimeoutSeconds: 120,
			RetryAfterSeconds:   0,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	header, payload, err := decodeExecuteRolloutFrame(frame)
	if err != nil {
		t.Fatal(err)
	}
	if header.Type != executeRolloutFrameDraining || header.RolloutID != "" {
		t.Fatalf("unexpected drain header %#v", header)
	}
	var notification executeRolloutDrainNotification
	if err := json.Unmarshal(payload, &notification); err != nil {
		t.Fatal(err)
	}
	if notification.DrainTimeoutSeconds != 120 {
		t.Fatalf("unexpected drain notification %#v", notification)
	}
}

func TestExecuteRolloutRequestOmitsUnsetJobID(t *testing.T) {
	payload, err := json.Marshal(executeRolloutRequest{RolloutID: "abc123"})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatal(err)
	}
	if _, found := body["job_id"]; found {
		t.Fatal("job_id must be omitted when it is unset")
	}
}

func TestExecuteRolloutNegotiatesV1AndV2(t *testing.T) {
	for _, protocol := range executeRolloutSubprotocols {
		t.Run(protocol, func(t *testing.T) {
			offered := make(chan string, 1)
			upgrader := websocket.Upgrader{Subprotocols: []string{protocol}}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				offered <- r.Header.Get("Sec-WebSocket-Protocol")
				connection, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Errorf("upgrade: %v", err)
					return
				}
				defer connection.Close()
				header, request := readExecuteRolloutTestRequest(t, connection)
				if request.JobID != "ftjob-abc123" {
					t.Errorf("job_id = %q, want ftjob-abc123", request.JobID)
				}
				writeExecuteRolloutTestFrame(t, connection, executeRolloutFrameHeader{
					Type:      executeRolloutFrameCompleted,
					RolloutID: header.RolloutID,
				}, successfulExecuteRolloutTestPayload(header.RolloutID))
			}))
			t.Cleanup(server.Close)
			stubExecuteRolloutV2Dialer(t, server.URL, nil)

			client := newRleClientWithCredential(
				"https://rle.test"+testFoundryProjectPath,
				&testTokenCredential{},
			)
			response, err := client.executeRolloutOverWebSocket(
				t.Context(),
				"code_rl",
				"1.0.0",
				"loom-token",
				executeRolloutRequest{RolloutID: "abc123", JobID: "ftjob-abc123"},
				nil,
			)
			if err != nil {
				t.Fatal(err)
			}
			if response.RolloutID != "abc123" {
				t.Fatalf("unexpected response %#v", response)
			}
			offeredProtocols := <-offered
			for _, expected := range executeRolloutSubprotocols {
				if !strings.Contains(offeredProtocols, expected) {
					t.Fatalf("expected the client to offer %s", expected)
				}
			}
		})
	}
}

func TestExecuteRolloutRotatesConnectionsWithoutReplayingAcceptedWork(t *testing.T) {
	var connectionCount atomic.Int32
	drainSent := make(chan struct{})
	replacementReceived := make(chan struct{})
	firstOldCompleted := make(chan struct{})
	replacementCompleted := make(chan struct{})
	requestCounts := make(chan string, 3)
	upgrader := websocket.Upgrader{Subprotocols: []string{executeRolloutSubprotocolV2}}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connectionNumber := connectionCount.Add(1)
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer connection.Close()

		switch connectionNumber {
		case 1:
			first, _ := readExecuteRolloutTestRequest(t, connection)
			second, _ := readExecuteRolloutTestRequest(t, connection)
			requestCounts <- first.RolloutID
			requestCounts <- second.RolloutID
			writeFragmentedExecuteRolloutTestFrame(t, connection, executeRolloutFrameHeader{
				Type: executeRolloutFrameDraining,
			}, executeRolloutDrainNotification{DrainTimeoutSeconds: 1})
			// Repeated notifications must not create duplicate replacements.
			writeExecuteRolloutTestFrame(t, connection, executeRolloutFrameHeader{
				Type: executeRolloutFrameDraining,
			}, executeRolloutDrainNotification{DrainTimeoutSeconds: 1})
			close(drainSent)

			<-replacementReceived
			writeExecuteRolloutTestFrame(t, connection, executeRolloutFrameHeader{
				Type:      executeRolloutFrameCompleted,
				RolloutID: second.RolloutID,
			}, successfulExecuteRolloutTestPayload(second.RolloutID))
			close(firstOldCompleted)
			<-replacementCompleted
			writeExecuteRolloutTestFrame(t, connection, executeRolloutFrameHeader{
				Type:      executeRolloutFrameCompleted,
				RolloutID: first.RolloutID,
			}, successfulExecuteRolloutTestPayload(first.RolloutID))
		case 2:
			header, _ := readExecuteRolloutTestRequest(t, connection)
			requestCounts <- header.RolloutID
			close(replacementReceived)
			<-firstOldCompleted
			writeExecuteRolloutTestFrame(t, connection, executeRolloutFrameHeader{
				Type:      executeRolloutFrameCompleted,
				RolloutID: header.RolloutID,
			}, successfulExecuteRolloutTestPayload(header.RolloutID))
			close(replacementCompleted)
		default:
			t.Errorf("unexpected replacement connection %d", connectionNumber)
		}
	}))
	t.Cleanup(server.Close)
	stubExecuteRolloutV2Dialer(t, server.URL, nil)

	client := newRleClientWithCredential(
		"https://rle.test"+testFoundryProjectPath,
		&testTokenCredential{},
	)
	manager := newExecuteRolloutConnectionManager(
		t.Context(),
		client,
		"code_rl",
		"1.0.0",
		"loom-token",
	)
	t.Cleanup(manager.Close)

	type result struct {
		id  string
		err error
	}
	results := make(chan result, 3)
	execute := func(id string) {
		response, err := manager.Execute(t.Context(), executeRolloutRequest{RolloutID: id}, nil)
		if err == nil {
			results <- result{id: response.RolloutID}
			return
		}
		results <- result{id: id, err: err}
	}
	go execute("rollout-a")
	go execute("rollout-b")

	select {
	case <-drainSent:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the drain notification")
	}
	waitForExecuteRolloutTestCondition(t, func() bool {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		return manager.active == nil && manager.drainingSocketCountLocked() == 1
	})
	go execute("rollout-c")

	seenResults := make(map[string]bool)
	for range 3 {
		select {
		case result := <-results:
			if result.err != nil {
				t.Fatalf("%s failed: %v", result.id, result.err)
			}
			seenResults[result.id] = true
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for rollout results")
		}
	}
	if len(seenResults) != 3 {
		t.Fatalf("expected all rollout results, got %#v", seenResults)
	}

	counts := map[string]int{}
	for range 3 {
		counts[<-requestCounts]++
	}
	for _, id := range []string{"rollout-a", "rollout-b", "rollout-c"} {
		if counts[id] != 1 {
			t.Fatalf("expected %s to be submitted once, got counts %#v", id, counts)
		}
	}
	if connectionCount.Load() != 2 {
		t.Fatalf("expected one replacement connection, got %d", connectionCount.Load())
	}
}

func TestExecuteRolloutRetriesCorrelatedServerDrainingOnReplacementForV1(t *testing.T) {
	var connectionCount atomic.Int32
	var requestCount atomic.Int32
	upgrader := websocket.Upgrader{Subprotocols: []string{executeRolloutSubprotocolV1}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connectionNumber := connectionCount.Add(1)
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer connection.Close()
		header, _ := readExecuteRolloutTestRequest(t, connection)
		requestCount.Add(1)
		if connectionNumber == 1 {
			writeExecuteRolloutTestFrame(t, connection, executeRolloutFrameHeader{
				Type:      executeRolloutFrameError,
				RolloutID: header.RolloutID,
			}, map[string]any{
				"code":                "ServerDraining",
				"message":             "use a replacement",
				"retry_after_seconds": 0,
			})
			return
		}
		writeExecuteRolloutTestFrame(t, connection, executeRolloutFrameHeader{
			Type:      executeRolloutFrameCompleted,
			RolloutID: header.RolloutID,
		}, successfulExecuteRolloutTestPayload(header.RolloutID))
	}))
	t.Cleanup(server.Close)
	stubExecuteRolloutDialer(t, server.URL, nil)

	client := newRleClientWithCredential(
		"https://rle.test"+testFoundryProjectPath,
		&testTokenCredential{},
	)
	response, err := client.executeRolloutOverWebSocket(
		t.Context(),
		"code_rl",
		"1.0.0",
		"loom-token",
		executeRolloutRequest{RolloutID: "retry-me"},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.RolloutID != "retry-me" || requestCount.Load() != 2 || connectionCount.Load() != 2 {
		t.Fatalf(
			"expected one replacement retry, response=%#v requests=%d connections=%d",
			response,
			requestCount.Load(),
			connectionCount.Load(),
		)
	}
}

func TestExecuteRolloutMovesAnUnstartedSendAfterDrain(t *testing.T) {
	var connectionCount atomic.Int32
	var requestCount atomic.Int32
	sendReached := make(chan struct{})
	allowSendCheck := make(chan struct{})
	drainSent := make(chan struct{})
	testDone := make(chan struct{})
	upgrader := websocket.Upgrader{Subprotocols: []string{executeRolloutSubprotocolV2}}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connectionNumber := connectionCount.Add(1)
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer connection.Close()
		if connectionNumber == 1 {
			<-sendReached
			writeExecuteRolloutTestFrame(t, connection, executeRolloutFrameHeader{
				Type: executeRolloutFrameDraining,
			}, executeRolloutDrainNotification{})
			close(drainSent)
			<-testDone
			return
		}
		header, _ := readExecuteRolloutTestRequest(t, connection)
		requestCount.Add(1)
		writeExecuteRolloutTestFrame(t, connection, executeRolloutFrameHeader{
			Type:      executeRolloutFrameCompleted,
			RolloutID: header.RolloutID,
		}, successfulExecuteRolloutTestPayload(header.RolloutID))
	}))
	t.Cleanup(server.Close)
	stubExecuteRolloutV2Dialer(t, server.URL, nil)

	previousBeforeSend := executeRolloutBeforeSend
	var hookCalls atomic.Int32
	executeRolloutBeforeSend = func() {
		if hookCalls.Add(1) == 1 {
			close(sendReached)
			<-allowSendCheck
		}
	}
	t.Cleanup(func() {
		executeRolloutBeforeSend = previousBeforeSend
		select {
		case <-testDone:
		default:
			close(testDone)
		}
	})

	client := newRleClientWithCredential(
		"https://rle.test"+testFoundryProjectPath,
		&testTokenCredential{},
	)
	manager := newExecuteRolloutConnectionManager(
		t.Context(),
		client,
		"code_rl",
		"1.0.0",
		"loom-token",
	)
	t.Cleanup(manager.Close)

	result := make(chan error, 1)
	go func() {
		_, err := manager.Execute(
			t.Context(),
			executeRolloutRequest{RolloutID: "move-before-send"},
			nil,
		)
		result <- err
	}()

	<-sendReached
	<-drainSent
	waitForExecuteRolloutTestCondition(t, func() bool {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		return manager.active == nil && manager.drainingSocketCountLocked() == 1
	})
	close(allowSendCheck)

	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the replacement result")
	}
	close(testDone)
	if requestCount.Load() != 1 || connectionCount.Load() != 2 {
		t.Fatalf(
			"expected one request only on the replacement, requests=%d connections=%d",
			requestCount.Load(),
			connectionCount.Load(),
		)
	}
}

func TestExecuteRolloutDoesNotReplayAmbiguousDisconnect(t *testing.T) {
	var connectionCount atomic.Int32
	upgrader := websocket.Upgrader{Subprotocols: []string{executeRolloutSubprotocolV2}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connectionCount.Add(1)
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		_, _ = readExecuteRolloutTestRequest(t, connection)
		_ = connection.UnderlyingConn().Close()
	}))
	t.Cleanup(server.Close)
	stubExecuteRolloutV2Dialer(t, server.URL, nil)

	client := newRleClientWithCredential(
		"https://rle.test"+testFoundryProjectPath,
		&testTokenCredential{},
	)
	_, err := client.executeRolloutOverWebSocket(
		t.Context(),
		"code_rl",
		"1.0.0",
		"loom-token",
		executeRolloutRequest{RolloutID: "ambiguous"},
		nil,
	)
	var outcomeUnknown *executeRolloutOutcomeUnknownError
	if !errors.As(err, &outcomeUnknown) {
		t.Fatalf("expected outcome unknown, got %v", err)
	}
	if connectionCount.Load() != 1 {
		t.Fatalf("ambiguous execution must not be replayed, got %d connections", connectionCount.Load())
	}
}

func TestExecuteRolloutExhaustsCorrelatedDrainRetries(t *testing.T) {
	var connectionCount atomic.Int32
	upgrader := websocket.Upgrader{Subprotocols: []string{executeRolloutSubprotocolV2}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connectionCount.Add(1)
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer connection.Close()
		header, _ := readExecuteRolloutTestRequest(t, connection)
		writeExecuteRolloutTestFrame(t, connection, executeRolloutFrameHeader{
			Type:      executeRolloutFrameError,
			RolloutID: header.RolloutID,
		}, map[string]string{
			"code":    "ServerDraining",
			"message": "replacement is also draining",
		})
	}))
	t.Cleanup(server.Close)
	stubExecuteRolloutV2Dialer(t, server.URL, nil)

	client := newRleClientWithCredential(
		"https://rle.test"+testFoundryProjectPath,
		&testTokenCredential{},
	)
	_, err := client.executeRolloutOverWebSocket(
		t.Context(),
		"code_rl",
		"1.0.0",
		"loom-token",
		executeRolloutRequest{RolloutID: "exhaust-drain-retries"},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "replacement is also draining") {
		t.Fatalf("expected exhausted ServerDraining error, got %v", err)
	}
	expectedConnections := int32(executeRolloutSubmissionMaxRetries + 1)
	if connectionCount.Load() != expectedConnections {
		t.Fatalf(
			"expected %d bounded submissions, got %d",
			expectedConnections,
			connectionCount.Load(),
		)
	}
}

func TestExecuteRolloutServerShuttingDownIsTerminal(t *testing.T) {
	var connectionCount atomic.Int32
	upgrader := websocket.Upgrader{Subprotocols: []string{executeRolloutSubprotocolV2}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connectionCount.Add(1)
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer connection.Close()
		header, _ := readExecuteRolloutTestRequest(t, connection)
		writeExecuteRolloutTestFrame(t, connection, executeRolloutFrameHeader{
			Type:      executeRolloutFrameError,
			RolloutID: header.RolloutID,
		}, map[string]string{
			"code":    "ServerShuttingDown",
			"message": "the execution reached the shutdown deadline",
		})
	}))
	t.Cleanup(server.Close)
	stubExecuteRolloutV2Dialer(t, server.URL, nil)

	client := newRleClientWithCredential(
		"https://rle.test"+testFoundryProjectPath,
		&testTokenCredential{},
	)
	_, err := client.executeRolloutOverWebSocket(
		t.Context(),
		"code_rl",
		"1.0.0",
		"loom-token",
		executeRolloutRequest{RolloutID: "shutdown"},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "shutdown deadline") {
		t.Fatalf("expected terminal shutdown error, got %v", err)
	}
	if connectionCount.Load() != 1 {
		t.Fatalf("ServerShuttingDown must not replay execution, got %d connections", connectionCount.Load())
	}
}

func TestExecuteRolloutDuplicateIDIsTerminal(t *testing.T) {
	var connectionCount atomic.Int32
	upgrader := websocket.Upgrader{Subprotocols: []string{executeRolloutSubprotocolV2}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connectionCount.Add(1)
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer connection.Close()
		header, _ := readExecuteRolloutTestRequest(t, connection)
		writeExecuteRolloutTestFrame(t, connection, executeRolloutFrameHeader{
			Type:      executeRolloutFrameError,
			RolloutID: header.RolloutID,
		}, map[string]string{
			"code":    "DuplicateRolloutId",
			"message": "the rollout ID is already in use",
		})
	}))
	t.Cleanup(server.Close)
	stubExecuteRolloutV2Dialer(t, server.URL, nil)

	client := newRleClientWithCredential(
		"https://rle.test"+testFoundryProjectPath,
		&testTokenCredential{},
	)
	_, err := client.executeRolloutOverWebSocket(
		t.Context(),
		"code_rl",
		"1.0.0",
		"loom-token",
		executeRolloutRequest{RolloutID: "duplicate"},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("expected duplicate rollout ID error, got %v", err)
	}
	if connectionCount.Load() != 1 {
		t.Fatalf("DuplicateRolloutId must not replay execution, got %d connections", connectionCount.Load())
	}
}

func TestExecuteRolloutReplacementFailureDoesNotFallBackToHTTP(t *testing.T) {
	var httpCalls atomic.Int32
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"rollout_id":"no-http-replay","reward":1,"success":true}`)
	}))
	t.Cleanup(httpServer.Close)

	upgrader := websocket.Upgrader{Subprotocols: []string{executeRolloutSubprotocolV2}}
	webSocketServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer connection.Close()
		header, _ := readExecuteRolloutTestRequest(t, connection)
		writeExecuteRolloutTestFrame(t, connection, executeRolloutFrameHeader{
			Type:      executeRolloutFrameError,
			RolloutID: header.RolloutID,
		}, map[string]string{
			"code":    "ServerDraining",
			"message": "retry on a replacement",
		})
	}))
	t.Cleanup(webSocketServer.Close)

	previousDial := dialExecuteRolloutWebSocket
	previousDelay := executeRolloutHandshakeRetryDelay
	var dialCount atomic.Int32
	dialExecuteRolloutWebSocket = func(
		ctx context.Context,
		_ string,
		headers http.Header,
	) (*websocket.Conn, *http.Response, error) {
		if dialCount.Add(1) == 1 {
			dialer := &websocket.Dialer{Subprotocols: executeRolloutSubprotocols}
			return dialer.DialContext(
				ctx,
				strings.Replace(webSocketServer.URL, "http://", "ws://", 1),
				headers,
			)
		}
		return nil, &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("replacement unavailable")),
		}, websocket.ErrBadHandshake
	}
	executeRolloutHandshakeRetryDelay = func(int, *http.Response) (time.Duration, error) {
		return 0, nil
	}
	t.Cleanup(func() {
		dialExecuteRolloutWebSocket = previousDial
		executeRolloutHandshakeRetryDelay = previousDelay
	})

	client := testRleClientForServer(t, httpServer.URL)
	_, err := client.executeRollout(
		t.Context(),
		"code_rl",
		"1.0.0",
		"loom-token",
		executeRolloutRequest{RolloutID: "no-http-replay"},
		nil,
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "replace Execute Rollout connection") {
		t.Fatalf("expected replacement failure, got %v", err)
	}
	if httpCalls.Load() != 0 {
		t.Fatalf("replacement failure must not fall back to HTTP, got %d calls", httpCalls.Load())
	}
}

func TestExecuteRolloutReplacementRetries503AndRetryAfter(t *testing.T) {
	var connectionCount atomic.Int32
	var dialCount atomic.Int32
	drained := make(chan struct{})
	upgrader := websocket.Upgrader{Subprotocols: []string{executeRolloutSubprotocolV2}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connectionNumber := connectionCount.Add(1)
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer connection.Close()
		header, _ := readExecuteRolloutTestRequest(t, connection)
		if connectionNumber == 1 {
			writeExecuteRolloutTestFrame(t, connection, executeRolloutFrameHeader{
				Type: executeRolloutFrameDraining,
			}, executeRolloutDrainNotification{})
			close(drained)
		}
		writeExecuteRolloutTestFrame(t, connection, executeRolloutFrameHeader{
			Type:      executeRolloutFrameCompleted,
			RolloutID: header.RolloutID,
		}, successfulExecuteRolloutTestPayload(header.RolloutID))
	}))
	t.Cleanup(server.Close)

	previousDial := dialExecuteRolloutWebSocket
	previousDelay := executeRolloutHandshakeRetryDelay
	dialExecuteRolloutWebSocket = func(
		ctx context.Context,
		_ string,
		headers http.Header,
	) (*websocket.Conn, *http.Response, error) {
		attempt := dialCount.Add(1)
		if attempt == 2 {
			return nil, &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Header:     http.Header{"Retry-After": []string{"0"}},
				Body:       io.NopCloser(strings.NewReader("draining")),
			}, websocket.ErrBadHandshake
		}
		dialer := &websocket.Dialer{Subprotocols: executeRolloutSubprotocols}
		return dialer.DialContext(ctx, strings.Replace(server.URL, "http://", "ws://", 1), headers)
	}
	executeRolloutHandshakeRetryDelay = func(int, *http.Response) (time.Duration, error) {
		return 0, nil
	}
	t.Cleanup(func() {
		dialExecuteRolloutWebSocket = previousDial
		executeRolloutHandshakeRetryDelay = previousDelay
	})

	client := newRleClientWithCredential(
		"https://rle.test"+testFoundryProjectPath,
		&testTokenCredential{},
	)
	manager := newExecuteRolloutConnectionManager(
		t.Context(),
		client,
		"code_rl",
		"1.0.0",
		"loom-token",
	)
	t.Cleanup(manager.Close)

	if _, err := manager.Execute(
		t.Context(),
		executeRolloutRequest{RolloutID: "before-drain"},
		nil,
	); err != nil {
		t.Fatal(err)
	}
	<-drained
	waitForExecuteRolloutTestCondition(t, func() bool {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		return manager.active == nil
	})
	if _, err := manager.Execute(
		t.Context(),
		executeRolloutRequest{RolloutID: "after-drain"},
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if dialCount.Load() != 3 || connectionCount.Load() != 2 {
		t.Fatalf(
			"expected a 503 followed by one replacement, dials=%d connections=%d",
			dialCount.Load(),
			connectionCount.Load(),
		)
	}
}

func TestExecuteRolloutRetryAfterParsingAndBound(t *testing.T) {
	now := time.Date(2026, 9, 25, 7, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		value    string
		expected time.Duration
	}{
		{name: "seconds", value: "7", expected: 7 * time.Second},
		{name: "date", value: now.Add(9 * time.Second).Format(http.TimeFormat), expected: 9 * time.Second},
		{name: "invalid", value: "later", expected: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := &http.Response{Header: http.Header{"Retry-After": []string{tc.value}}}
			if actual := executeRolloutRetryAfter(response, now); actual != tc.expected {
				t.Fatalf("expected %s, got %s", tc.expected, actual)
			}
		})
	}

	response := &http.Response{Header: http.Header{"Retry-After": []string{"120"}}}
	delay, err := defaultExecuteRolloutHandshakeRetryDelay(0, response)
	if err != nil {
		t.Fatal(err)
	}
	if delay != executeRolloutRetryAfterMax {
		t.Fatalf("expected bounded Retry-After %s, got %s", executeRolloutRetryAfterMax, delay)
	}
}

func readExecuteRolloutTestRequest(
	t *testing.T,
	connection *websocket.Conn,
) (executeRolloutFrameHeader, executeRolloutRequest) {
	t.Helper()
	messageType, message, err := connection.ReadMessage()
	if err != nil {
		t.Fatalf("read Execute Rollout request: %v", err)
	}
	if messageType != websocket.BinaryMessage {
		t.Fatalf("expected a binary Execute Rollout request, got %d", messageType)
	}
	header, payload, err := decodeExecuteRolloutFrame(message)
	if err != nil {
		t.Fatal(err)
	}
	var request executeRolloutRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		t.Fatal(err)
	}
	return header, request
}

func writeExecuteRolloutTestFrame(
	t *testing.T,
	connection *websocket.Conn,
	header executeRolloutFrameHeader,
	payload any,
) {
	t.Helper()
	frame, err := encodeExecuteRolloutFrame(header, payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		t.Fatalf("write Execute Rollout frame: %v", err)
	}
}

func writeFragmentedExecuteRolloutTestFrame(
	t *testing.T,
	connection *websocket.Conn,
	header executeRolloutFrameHeader,
	payload any,
) {
	t.Helper()
	frame, err := encodeExecuteRolloutFrame(header, payload)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := connection.NextWriter(websocket.BinaryMessage)
	if err != nil {
		t.Fatal(err)
	}
	middle := len(frame) / 2
	if _, err := writer.Write(frame[:middle]); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(frame[middle:]); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
}

func successfulExecuteRolloutTestPayload(rolloutID string) map[string]any {
	return map[string]any{
		"rollout_id": rolloutID,
		"reward":     1.0,
		"success":    true,
	}
}

func stubExecuteRolloutV2Dialer(
	t *testing.T,
	serverURL string,
	dialCount *atomic.Int32,
) {
	t.Helper()
	previous := dialExecuteRolloutWebSocket
	dialExecuteRolloutWebSocket = func(
		ctx context.Context,
		_ string,
		headers http.Header,
	) (*websocket.Conn, *http.Response, error) {
		if dialCount != nil {
			dialCount.Add(1)
		}
		dialer := &websocket.Dialer{Subprotocols: executeRolloutSubprotocols}
		return dialer.DialContext(ctx, strings.Replace(serverURL, "http://", "ws://", 1), headers)
	}
	t.Cleanup(func() { dialExecuteRolloutWebSocket = previous })
}

func waitForExecuteRolloutTestCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for Execute Rollout condition")
		}
		time.Sleep(time.Millisecond)
	}
}
