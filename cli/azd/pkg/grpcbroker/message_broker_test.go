// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcbroker

import (
	"context"
	"errors"
	"io"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Test message types
type TestMessage struct {
	RequestId    string
	Error        error
	Data         string
	InnerMsg     any
	IsProgress   bool
	ProgressText string
}

// Test request/response types for handler testing
type TestRequest struct {
	Value string
}

type TestResponse struct {
	Result string
}

// SimulatedBidiStream simulates a bidirectional gRPC stream with two endpoints
type SimulatedBidiStream struct {
	clientToServer chan *TestMessage
	serverToClient chan *TestMessage
	done           chan struct{}
	closed         bool
	mu             sync.Mutex
}

func NewSimulatedBidiStream() *SimulatedBidiStream {
	return &SimulatedBidiStream{
		clientToServer: make(chan *TestMessage, 10),
		serverToClient: make(chan *TestMessage, 10),
		done:           make(chan struct{}),
	}
}

func (s *SimulatedBidiStream) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.done)
	}
}

// ClientStream returns a stream interface for the client side
func (s *SimulatedBidiStream) ClientStream() BidiStream[TestMessage] {
	return &clientSideStream{sim: s}
}

// ServerStream returns a stream interface for the server side
func (s *SimulatedBidiStream) ServerStream() BidiStream[TestMessage] {
	return &serverSideStream{sim: s}
}

type clientSideStream struct {
	sim *SimulatedBidiStream
}

func (c *clientSideStream) Send(msg *TestMessage) error {
	c.sim.mu.Lock()
	closed := c.sim.closed
	c.sim.mu.Unlock()
	if closed {
		return io.EOF
	}
	select {
	case c.sim.clientToServer <- msg:
		return nil
	case <-c.sim.done:
		return io.EOF
	}
}

func (c *clientSideStream) Recv() (*TestMessage, error) {
	select {
	case msg := <-c.sim.serverToClient:
		return msg, nil
	case <-c.sim.done:
		return nil, io.EOF
	}
}

type serverSideStream struct {
	sim *SimulatedBidiStream
}

func (s *serverSideStream) Send(msg *TestMessage) error {
	s.sim.mu.Lock()
	closed := s.sim.closed
	s.sim.mu.Unlock()
	if closed {
		return io.EOF
	}
	select {
	case s.sim.serverToClient <- msg:
		return nil
	case <-s.sim.done:
		return io.EOF
	}
}

func (s *serverSideStream) Recv() (*TestMessage, error) {
	select {
	case msg := <-s.sim.clientToServer:
		return msg, nil
	case <-s.sim.done:
		return nil, io.EOF
	}
}

// SimpleMessageEnvelope is a simple implementation of MessageEnvelope for testing
type SimpleMessageEnvelope struct{}

func (e *SimpleMessageEnvelope) GetRequestId(ctx context.Context, msg *TestMessage) string {
	return msg.RequestId
}

func (e *SimpleMessageEnvelope) SetRequestId(ctx context.Context, msg *TestMessage, id string) {
	msg.RequestId = id
}

func (e *SimpleMessageEnvelope) GetError(msg *TestMessage) error {
	return msg.Error
}

func (e *SimpleMessageEnvelope) SetError(msg *TestMessage, err error) {
	msg.Error = err
}

func (e *SimpleMessageEnvelope) GetInnerMessage(msg *TestMessage) any {
	if msg == nil {
		return nil
	}
	if msg.InnerMsg != nil {
		return msg.InnerMsg
	}
	if msg.Error != nil {
		return nil
	}
	// Fallback: try to infer from data
	return &TestRequest{Value: msg.Data}
}

func (e *SimpleMessageEnvelope) IsProgressMessage(msg *TestMessage) bool {
	return msg.IsProgress
}

func (e *SimpleMessageEnvelope) GetProgressMessage(msg *TestMessage) string {
	return msg.ProgressText
}

func (e *SimpleMessageEnvelope) CreateProgressMessage(requestId string, message string) *TestMessage {
	return &TestMessage{
		RequestId:    requestId,
		IsProgress:   true,
		ProgressText: message,
	}
}

type persistentTestMessageEnvelope struct {
	SimpleMessageEnvelope
}

func (e *persistentTestMessageEnvelope) PreserveHandlerContext(
	_ context.Context,
	msg *TestMessage,
) bool {
	return msg.Data == "persistent"
}

// TestOn_RegistersHandler tests that handlers are registered correctly
func TestOn_RegistersHandler(t *testing.T) {
	sim := NewSimulatedBidiStream()
	defer sim.Close()

	envelope := &SimpleMessageEnvelope{}
	broker := NewMessageBroker(sim.ServerStream(), envelope, "test", nil)

	// Valid handler with context and request only
	handler := func(ctx context.Context, req *TestRequest) (*TestMessage, error) {
		return &TestMessage{Data: req.Value}, nil
	}

	err := broker.On(handler)
	require.NoError(t, err)

	// Verify handler was registered
	requestType := reflect.TypeFor[*TestRequest]()
	_, ok := broker.handlers.Load(requestType)
	assert.True(t, ok, "Handler should be registered")
}

// TestOn_RegistersHandlerWithProgress tests that handlers with progress callback are registered correctly
func TestOn_RegistersHandlerWithProgress(t *testing.T) {
	sim := NewSimulatedBidiStream()
	defer sim.Close()

	envelope := &SimpleMessageEnvelope{}
	broker := NewMessageBroker(sim.ServerStream(), envelope, "test", nil)

	// Valid handler with progress callback
	handler := func(ctx context.Context, req *TestRequest, progress ProgressFunc) (*TestMessage, error) {
		progress("working...")
		return &TestMessage{Data: req.Value}, nil
	}

	err := broker.On(handler)
	require.NoError(t, err)

	// Verify handler was registered with progress flag
	requestType := reflect.TypeFor[*TestRequest]()
	wrapper, ok := broker.handlers.Load(requestType)
	require.True(t, ok, "Handler should be registered")

	handlerWrapper := wrapper
	assert.True(t, handlerWrapper.hasProgress, "Handler should be marked as having progress")
	assert.Equal(t, 2, handlerWrapper.progressIndex, "Progress parameter should be at index 2")
}

// TestOn_InvalidHandler tests validation of invalid handler signatures
func TestOn_InvalidHandler(t *testing.T) {
	sim := NewSimulatedBidiStream()
	defer sim.Close()

	envelope := &SimpleMessageEnvelope{}
	broker := NewMessageBroker(sim.ServerStream(), envelope, "test", nil)

	tests := []struct {
		name    string
		handler any
		wantErr string
	}{
		{
			name:    "not a function",
			handler: "not a function",
			wantErr: "handler must be a function",
		},
		{
			name:    "wrong number of parameters",
			handler: func() {},
			wantErr: "handler must have 2 or 3 parameters",
		},
		{
			name:    "first param not context",
			handler: func(s string, req *TestRequest) (*TestMessage, error) { return nil, nil },
			wantErr: "first parameter must be context.Context",
		},
		{
			name:    "second param not pointer",
			handler: func(ctx context.Context, req TestRequest) (*TestMessage, error) { return nil, nil },
			wantErr: "request type must be a pointer",
		},
		{
			name: "third param not ProgressFunc",
			handler: func(ctx context.Context, req *TestRequest, s string) (*TestMessage, error) {
				return nil, nil
			},
			wantErr: "third parameter must be ProgressFunc",
		},
		{
			name:    "wrong number of return values",
			handler: func(ctx context.Context, req *TestRequest) error { return nil },
			wantErr: "handler must return 2 values",
		},
		{
			name:    "second return not error",
			handler: func(ctx context.Context, req *TestRequest) (*TestMessage, string) { return nil, "" },
			wantErr: "second return value must be error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := broker.On(tt.handler)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// TestSend_Success tests successful fire-and-forget send
func TestSend_Success(t *testing.T) {
	sim := NewSimulatedBidiStream()
	defer sim.Close()

	envelope := &SimpleMessageEnvelope{}
	clientBroker := NewMessageBroker(sim.ClientStream(), envelope, "client", nil)

	ctx := t.Context()
	msg := &TestMessage{RequestId: "fire-forget-123", Data: "notification"}

	err := clientBroker.Send(ctx, msg)
	require.NoError(t, err)

	// Verify message was sent to server
	select {
	case received := <-sim.clientToServer:
		assert.Equal(t, msg, received)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("message not received on stream")
	}
}

// TestSendAndWait_NoRequestId tests that SendAndWait fails when request ID is missing
func TestSendAndWait_NoRequestId(t *testing.T) {
	sim := NewSimulatedBidiStream()
	defer sim.Close()

	envelope := &SimpleMessageEnvelope{}
	clientBroker := NewMessageBroker(sim.ClientStream(), envelope, "client", nil)

	ctx := t.Context()
	requestMsg := &TestMessage{Data: "request"} // No RequestId

	_, err := clientBroker.SendAndWait(ctx, requestMsg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "message must have a RequestId")
}

// TestEndToEnd_ClientSendsServerResponds tests full bidirectional flow
func TestEndToEnd_ClientSendsServerResponds(t *testing.T) {
	sim := NewSimulatedBidiStream()
	defer sim.Close()

	envelope := &SimpleMessageEnvelope{}
	clientBroker := NewMessageBroker(sim.ClientStream(), envelope, "client", nil)
	serverBroker := NewMessageBroker(sim.ServerStream(), envelope, "server", nil)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// Register server handler
	handlerCalled := make(chan *TestRequest, 1)
	handler := func(ctx context.Context, req *TestRequest) (*TestMessage, error) {
		handlerCalled <- req
		return &TestMessage{
			InnerMsg: &TestResponse{Result: "processed: " + req.Value},
		}, nil
	}
	err := serverBroker.On(handler)
	require.NoError(t, err)

	// Start server broker
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- serverBroker.Run(ctx)
	}()

	// Start client broker to receive responses
	clientDone := make(chan error, 1)
	go func() {
		clientDone <- clientBroker.Run(ctx)
	}()

	// Wait until both brokers are ready to process messages.
	require.NoError(t, serverBroker.Ready(ctx))
	require.NoError(t, clientBroker.Ready(ctx))

	// Client sends request and waits for response
	requestMsg := &TestMessage{
		RequestId: "req-123",
		InnerMsg:  &TestRequest{Value: "test-value"},
	}

	resp, err := clientBroker.SendAndWait(ctx, requestMsg)
	require.NoError(t, err)
	assert.NotNil(t, resp)

	// Verify handler was called with correct data
	select {
	case req := <-handlerCalled:
		assert.Equal(t, "test-value", req.Value)
	case <-time.After(1 * time.Second):
		t.Fatal("handler not called")
	}

	// Clean up
	cancel()
	sim.Close()
	<-serverDone
	<-clientDone
}

// TestEndToEnd_SendAndWaitWithProgress tests send with progress updates
func TestEndToEnd_SendAndWaitWithProgress(t *testing.T) {
	sim := NewSimulatedBidiStream()
	defer sim.Close()

	envelope := &SimpleMessageEnvelope{}
	clientBroker := NewMessageBroker(sim.ClientStream(), envelope, "client", nil)
	serverBroker := NewMessageBroker(sim.ServerStream(), envelope, "server", nil)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// Register handler with progress
	handler := func(ctx context.Context, req *TestRequest, progress ProgressFunc) (*TestMessage, error) {
		progress("Starting...")
		// justified: these sleeps simulate work between progress updates so the test
		// can observe the intermediate progress messages being delivered to the client
		// before the handler returns its final response.
		time.Sleep(10 * time.Millisecond)
		progress("50% done")
		time.Sleep(10 * time.Millisecond)
		progress("Almost there...")
		time.Sleep(10 * time.Millisecond) // Give time for progress message to be sent before returning
		return &TestMessage{
			InnerMsg: &TestResponse{Result: "done"},
		}, nil
	}
	err := serverBroker.On(handler)
	require.NoError(t, err)

	// Start server
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- serverBroker.Run(ctx)
	}()

	// Start client to receive responses
	clientDone := make(chan error, 1)
	go func() {
		clientDone <- clientBroker.Run(ctx)
	}()

	// Wait for both brokers' Run() loops to enter the receive loop.
	require.NoError(t, serverBroker.Ready(ctx))
	require.NoError(t, clientBroker.Ready(ctx))

	// Client sends with progress callback
	progressUpdates := []string{}
	var progressMu sync.Mutex
	progressCb := func(msg string) {
		progressMu.Lock()
		progressUpdates = append(progressUpdates, msg)
		progressMu.Unlock()
	}

	requestMsg := &TestMessage{
		RequestId: "progress-req-123",
		InnerMsg:  &TestRequest{Value: "process-me"},
	}

	resp, err := clientBroker.SendAndWaitWithProgress(ctx, requestMsg, progressCb)
	require.NoError(t, err)
	assert.NotNil(t, resp)

	// Give a small delay to ensure all progress messages are delivered
	// (the final progress message might still be in flight when SendAndWaitWithProgress returns)
	time.Sleep(20 * time.Millisecond)

	// Verify progress updates were received
	progressMu.Lock()
	assert.Equal(t, []string{"Starting...", "50% done", "Almost there..."}, progressUpdates)
	progressMu.Unlock()

	// Clean up
	cancel()
	sim.Close()
	<-serverDone
	<-clientDone
}

// TestEndToEnd_HandlerReturnsError tests error propagation from handler to client
func TestEndToEnd_HandlerReturnsError(t *testing.T) {
	sim := NewSimulatedBidiStream()
	defer sim.Close()

	envelope := &SimpleMessageEnvelope{}
	clientBroker := NewMessageBroker(sim.ClientStream(), envelope, "client", nil)
	serverBroker := NewMessageBroker(sim.ServerStream(), envelope, "server", nil)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// Register handler that returns an error
	expectedErr := errors.New("handler failed")
	handler := func(ctx context.Context, req *TestRequest) (*TestMessage, error) {
		return nil, expectedErr
	}
	err := serverBroker.On(handler)
	require.NoError(t, err)

	// Start server
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- serverBroker.Run(ctx)
	}()

	// Start client to receive responses
	clientDone := make(chan error, 1)
	go func() {
		clientDone <- clientBroker.Run(ctx)
	}()

	// Wait for both brokers' Run() loops to enter the receive loop.
	require.NoError(t, serverBroker.Ready(ctx))
	require.NoError(t, clientBroker.Ready(ctx))

	// Client sends request
	requestMsg := &TestMessage{
		RequestId: "error-req-123",
		InnerMsg:  &TestRequest{Value: "fail-me"},
	}

	_, err = clientBroker.SendAndWait(ctx, requestMsg)
	require.Error(t, err)
	assert.Equal(t, expectedErr.Error(), err.Error())

	// Clean up
	cancel()
	sim.Close()
	<-serverDone
	<-clientDone
}

// TestEndToEnd_MultipleHandlers tests that different message types route to correct handlers
func TestEndToEnd_MultipleHandlers(t *testing.T) {
	sim := NewSimulatedBidiStream()
	defer sim.Close()

	envelope := &SimpleMessageEnvelope{}
	serverBroker := NewMessageBroker(sim.ServerStream(), envelope, "server", nil)
	clientBroker := NewMessageBroker(sim.ClientStream(), envelope, "client", nil)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// Define alternate request type
	type AlternateRequest struct {
		ID int
	}

	// Register two handlers for different types
	handler1Called := make(chan string, 1)
	handler1 := func(ctx context.Context, req *TestRequest) (*TestMessage, error) {
		handler1Called <- req.Value
		return &TestMessage{InnerMsg: &TestResponse{Result: "handler1"}}, nil
	}

	handler2Called := make(chan int, 1)
	handler2 := func(ctx context.Context, req *AlternateRequest) (*TestMessage, error) {
		handler2Called <- req.ID
		return &TestMessage{InnerMsg: &TestResponse{Result: "handler2"}}, nil
	}

	err := serverBroker.On(handler1)
	require.NoError(t, err)

	err = serverBroker.On(handler2)
	require.NoError(t, err)

	// Start server
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- serverBroker.Run(ctx)
	}()

	// Start client to receive responses
	clientDone := make(chan error, 1)
	go func() {
		clientDone <- clientBroker.Run(ctx)
	}()

	// Wait for both brokers' Run() loops to enter the receive loop.
	require.NoError(t, serverBroker.Ready(ctx))
	require.NoError(t, clientBroker.Ready(ctx))

	// Send first request type
	req1 := &TestMessage{
		RequestId: "req1",
		InnerMsg:  &TestRequest{Value: "value1"},
	}
	resp1, err := clientBroker.SendAndWait(ctx, req1)
	require.NoError(t, err)
	assert.NotNil(t, resp1)

	// Send second request type
	req2 := &TestMessage{
		RequestId: "req2",
		InnerMsg:  &AlternateRequest{ID: 42},
	}
	resp2, err := clientBroker.SendAndWait(ctx, req2)
	require.NoError(t, err)
	assert.NotNil(t, resp2)

	// Verify both handlers were called
	select {
	case val := <-handler1Called:
		assert.Equal(t, "value1", val)
	case <-time.After(1 * time.Second):
		t.Fatal("handler1 not called")
	}

	select {
	case id := <-handler2Called:
		assert.Equal(t, 42, id)
	case <-time.After(1 * time.Second):
		t.Fatal("handler2 not called")
	}

	// Clean up
	cancel()
	sim.Close()
	<-serverDone
	<-clientDone
}

// TestRun_ContextCancellation tests that Run handles context cancellation
func TestRun_ContextCancellation(t *testing.T) {
	sim := NewSimulatedBidiStream()
	defer sim.Close()

	envelope := &SimpleMessageEnvelope{}
	broker := NewMessageBroker(sim.ServerStream(), envelope, "test", nil)

	ctx, cancel := context.WithCancel(t.Context())

	// Start broker
	done := make(chan error, 1)
	go func() {
		done <- broker.Run(ctx)
	}()

	// Cancel context
	cancel()

	// Verify Run exits with context canceled error
	select {
	case err := <-done:
		assert.Equal(t, context.Canceled, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not exit after context cancellation")
	}
}

// TestRun_GracefulShutdown_EOF tests EOF handling
func TestRun_GracefulShutdown_EOF(t *testing.T) {
	sim := NewSimulatedBidiStream()

	envelope := &SimpleMessageEnvelope{}
	broker := NewMessageBroker(sim.ServerStream(), envelope, "test", nil)

	ctx := t.Context()

	// Close stream immediately to cause EOF
	sim.Close()

	// Run should exit gracefully
	err := broker.Run(ctx)
	require.NoError(t, err)
}

// TestClose_ClosesAllWaiters tests that Close properly cleans up.
func TestClose_ClosesAllWaiters(t *testing.T) {
	sim := NewSimulatedBidiStream()
	defer sim.Close()

	envelope := &SimpleMessageEnvelope{}
	broker := NewMessageBroker(sim.ClientStream(), envelope, "test", nil)

	ctx := t.Context()

	errCh := make(chan error, 2)

	// Start some SendAndWait operations that will register waiters.
	go func() {
		msg := &TestMessage{RequestId: "req1", InnerMsg: &TestRequest{Value: "test"}}
		_, err := broker.SendAndWait(ctx, msg)
		errCh <- err
	}()

	go func() {
		msg := &TestMessage{RequestId: "req2", InnerMsg: &TestRequest{Value: "test"}}
		_, err := broker.SendAndWait(ctx, msg)
		errCh <- err
	}()

	// Wait for both response waiters to register.
	require.Eventually(t, func() bool {
		count := 0
		broker.responseWaiters.Range(func(_ string, _ *responseWaiter[TestMessage]) bool {
			count++
			return true
		})
		return count == 2
	}, time.Second, 5*time.Millisecond, "both response waiters should register")

	broker.Close()

	count := 0
	broker.responseWaiters.Range(func(_ string, _ *responseWaiter[TestMessage]) bool {
		count++
		return true
	})
	assert.Equal(t, 0, count, "all response waiters should be removed")

	for range 2 {
		select {
		case err := <-errCh:
			require.ErrorIs(t, err, errMessageBrokerClosed)
		case <-time.After(time.Second):
			t.Fatal("pending request did not return after broker close")
		}
	}

	_, err := broker.SendAndWait(t.Context(), &TestMessage{
		RequestId: "after-close",
		InnerMsg:  &TestRequest{Value: "test"},
	})
	require.ErrorIs(t, err, errMessageBrokerClosed)
}

func TestRegisterResponseWaiter_RejectsDuplicateCorrelationId(t *testing.T) {
	sim := NewSimulatedBidiStream()
	defer sim.Close()

	broker := NewMessageBroker(sim.ClientStream(), &SimpleMessageEnvelope{}, "client", nil)
	first := newResponseWaiter[TestMessage](1)
	second := newResponseWaiter[TestMessage](1)

	require.NoError(t, broker.registerResponseWaiter("shared-request", first))
	t.Cleanup(func() {
		broker.unregisterResponseWaiter("shared-request", first)
	})

	err := broker.registerResponseWaiter("shared-request", second)

	require.ErrorIs(t, err, errRequestAlreadyPending)
	second.close()
}

func TestResponseWaiter_FullProgressQueueKeepsFinalResponse(t *testing.T) {
	waiter := newResponseWaiter[TestMessage](1)
	t.Cleanup(waiter.close)

	progress := &TestMessage{RequestId: "request", IsProgress: true, ProgressText: "first"}
	droppedProgress := &TestMessage{RequestId: "request", IsProgress: true, ProgressText: "second"}
	final := &TestMessage{RequestId: "request", InnerMsg: &TestResponse{Result: "done"}}

	require.True(t, waiter.send(progress, true))
	require.False(t, waiter.send(droppedProgress, true))
	require.True(t, waiter.send(final, false))

	actualProgress, ok := waiter.receive()
	require.True(t, ok)
	require.Same(t, progress, actualProgress)

	actualFinal, ok := waiter.receive()
	require.True(t, ok)
	require.Same(t, final, actualFinal)
}

func TestRun_FullProgressWaiterDoesNotBlockCancellation(t *testing.T) {
	sim := NewSimulatedBidiStream()
	defer sim.Close()

	broker := NewMessageBroker(sim.ServerStream(), &SimpleMessageEnvelope{}, "server", nil)
	slowWaiter := newResponseWaiter[TestMessage](1)
	require.NoError(t, broker.registerResponseWaiter("slow-request", slowWaiter))
	t.Cleanup(func() {
		broker.unregisterResponseWaiter("slow-request", slowWaiter)
	})

	handlerStarted := make(chan struct{})
	handlerCanceled := make(chan error, 1)
	require.NoError(t, broker.On(func(ctx context.Context, _ *TestRequest) (*TestMessage, error) {
		close(handlerStarted)
		<-ctx.Done()
		handlerCanceled <- context.Cause(ctx)
		return nil, ctx.Err()
	}))

	brokerCtx, cancelBroker := context.WithCancel(t.Context())
	defer cancelBroker()
	go func() {
		_ = broker.Run(brokerCtx)
	}()
	require.NoError(t, broker.Ready(t.Context()))

	clientStream := sim.ClientStream()
	require.NoError(t, clientStream.Send(&TestMessage{
		RequestId: "cancel-request",
		InnerMsg:  &TestRequest{Value: "work"},
	}))
	select {
	case <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}

	require.NoError(t, clientStream.Send(&TestMessage{
		RequestId:    "slow-request",
		IsProgress:   true,
		ProgressText: "first",
	}))
	require.NoError(t, clientStream.Send(&TestMessage{
		RequestId:    "slow-request",
		IsProgress:   true,
		ProgressText: "second",
	}))
	require.NoError(t, clientStream.Send(&TestMessage{
		RequestId: "cancel-request",
		Error:     context.Canceled,
	}))

	select {
	case cause := <-handlerCanceled:
		require.ErrorIs(t, cause, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("full progress waiter blocked cancellation delivery")
	}
}

func TestDuplicateRequestIds_DoNotCancelActiveHandlers(t *testing.T) {
	sim := NewSimulatedBidiStream()
	defer sim.Close()

	broker := NewMessageBroker(sim.ServerStream(), &SimpleMessageEnvelope{}, "server", nil)
	started := make(chan struct{}, 2)
	canceled := make(chan error, 2)
	release := make(chan struct{})
	require.NoError(t, broker.On(func(ctx context.Context, req *TestRequest) (*TestMessage, error) {
		started <- struct{}{}
		select {
		case <-ctx.Done():
			canceled <- context.Cause(ctx)
			return nil, ctx.Err()
		case <-release:
			return &TestMessage{InnerMsg: &TestResponse{Result: req.Value}}, nil
		}
	}))

	for _, value := range []string{"first", "second"} {
		broker.processMessage(t.Context(), &TestMessage{
			RequestId: "shared-request",
			InnerMsg:  &TestRequest{Value: value},
		})
	}

	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("handler did not start")
		}
	}

	select {
	case cause := <-canceled:
		t.Fatalf("duplicate request id canceled an active handler: %v", cause)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	for range 2 {
		select {
		case <-sim.serverToClient:
		case <-time.After(time.Second):
			t.Fatal("handler response was not sent")
		}
	}
}

func TestEndToEnd_CancellationCancelsMatchingHandler(t *testing.T) {
	sim := NewSimulatedBidiStream()
	defer sim.Close()

	clientBroker := NewMessageBroker(
		sim.ClientStream(),
		&SimpleMessageEnvelope{},
		"client",
		nil,
		WithCancellationGracePeriod(time.Second),
	)
	serverBroker := NewMessageBroker(
		sim.ServerStream(),
		&SimpleMessageEnvelope{},
		"server",
		nil,
		WithCancellationGracePeriod(time.Second),
	)

	brokerCtx, stopBrokers := context.WithCancel(t.Context())
	defer stopBrokers()

	handlerStarted := make(chan struct{})
	handlerCause := make(chan error, 1)
	require.NoError(t, serverBroker.On(func(ctx context.Context, req *TestRequest) (*TestMessage, error) {
		close(handlerStarted)
		<-ctx.Done()
		handlerCause <- context.Cause(ctx)
		return nil, ctx.Err()
	}))

	go func() {
		_ = serverBroker.Run(brokerCtx)
	}()
	go func() {
		_ = clientBroker.Run(brokerCtx)
	}()
	require.NoError(t, serverBroker.Ready(t.Context()))
	require.NoError(t, clientBroker.Ready(t.Context()))

	requestCtx, cancelRequest := context.WithCancel(t.Context())
	errCh := make(chan error, 1)
	go func() {
		_, err := clientBroker.SendAndWait(requestCtx, &TestMessage{
			RequestId: "cancel-request",
			InnerMsg:  &TestRequest{Value: "work"},
		})
		errCh <- err
	}()

	select {
	case <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	cancelRequest()

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("canceled request did not return")
	}

	select {
	case cause := <-handlerCause:
		require.ErrorIs(t, cause, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("handler context was not canceled")
	}
}

func TestEndToEnd_DeadlineCancelsMatchingHandler(t *testing.T) {
	sim := NewSimulatedBidiStream()
	defer sim.Close()

	clientBroker := NewMessageBroker(
		sim.ClientStream(),
		&SimpleMessageEnvelope{},
		"client",
		nil,
		WithCancellationGracePeriod(time.Second),
	)
	serverBroker := NewMessageBroker(
		sim.ServerStream(),
		&SimpleMessageEnvelope{},
		"server",
		nil,
		WithCancellationGracePeriod(time.Second),
	)

	brokerCtx, stopBrokers := context.WithCancel(t.Context())
	defer stopBrokers()

	handlerStarted := make(chan struct{})
	handlerCause := make(chan error, 1)
	require.NoError(t, serverBroker.On(func(ctx context.Context, req *TestRequest) (*TestMessage, error) {
		close(handlerStarted)
		<-ctx.Done()
		handlerCause <- context.Cause(ctx)
		return nil, ctx.Err()
	}))

	go func() {
		_ = serverBroker.Run(brokerCtx)
	}()
	go func() {
		_ = clientBroker.Run(brokerCtx)
	}()
	require.NoError(t, serverBroker.Ready(t.Context()))
	require.NoError(t, clientBroker.Ready(t.Context()))

	requestCtx, cancelRequest := context.WithCancelCause(t.Context())
	errCh := make(chan error, 1)
	go func() {
		_, err := clientBroker.SendAndWait(requestCtx, &TestMessage{
			RequestId: "deadline-request",
			InnerMsg:  &TestRequest{Value: "work"},
		})
		errCh <- err
	}()

	select {
	case <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}

	cancelRequest(context.DeadlineExceeded)

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(time.Second):
		t.Fatal("timed-out request did not return")
	}

	select {
	case cause := <-handlerCause:
		require.ErrorIs(t, cause, context.DeadlineExceeded)
	case <-time.After(time.Second):
		t.Fatal("handler context did not receive the deadline")
	}
}

func TestClose_CancelsActiveHandlers(t *testing.T) {
	sim := NewSimulatedBidiStream()
	defer sim.Close()

	broker := NewMessageBroker(sim.ServerStream(), &SimpleMessageEnvelope{}, "server", nil)
	handlerStarted := make(chan struct{})
	handlerStopped := make(chan error, 1)
	require.NoError(t, broker.On(func(ctx context.Context, req *TestRequest) (*TestMessage, error) {
		close(handlerStarted)
		<-ctx.Done()
		handlerStopped <- ctx.Err()
		return nil, ctx.Err()
	}))

	brokerCtx, cancelBroker := context.WithCancel(t.Context())
	defer cancelBroker()
	go func() {
		_ = broker.Run(brokerCtx)
	}()
	require.NoError(t, broker.Ready(t.Context()))

	sim.clientToServer <- &TestMessage{
		RequestId: "active-request",
		InnerMsg:  &TestRequest{Value: "work"},
	}
	select {
	case <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}

	broker.Close()
	select {
	case err := <-handlerStopped:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("broker close did not cancel handler")
	}
}

func TestPersistentHandlerContext_RemainsUntilStreamCancellation(t *testing.T) {
	sim := NewSimulatedBidiStream()
	defer sim.Close()

	broker := NewMessageBroker(sim.ServerStream(), &persistentTestMessageEnvelope{}, "server", nil)
	handlerCtx := make(chan context.Context, 1)
	require.NoError(t, broker.On(func(ctx context.Context, _ *TestRequest) (*TestMessage, error) {
		handlerCtx <- ctx
		return nil, nil
	}))

	streamCtx, cancelStream := context.WithCancel(t.Context())
	broker.processMessage(streamCtx, &TestMessage{
		RequestId: "persistent-request",
		Data:      "persistent",
		InnerMsg:  &TestRequest{Value: "register"},
	})

	var ctx context.Context
	select {
	case ctx = <-handlerCtx:
	case <-time.After(time.Second):
		t.Fatal("handler did not run")
	}

	require.NoError(t, ctx.Err())
	cancelStream()
	require.Eventually(t, func() bool {
		return errors.Is(ctx.Err(), context.Canceled)
	}, time.Second, 10*time.Millisecond)
}

// TestEndToEnd_HandlerPanic verifies that when a handler panics, the client receives
// an error response instead of hanging forever
func TestEndToEnd_HandlerPanic(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	// Create simulated stream and both client/server brokers
	stream := NewSimulatedBidiStream()
	clientBroker := NewMessageBroker(stream.ClientStream(), &SimpleMessageEnvelope{}, "client", nil)
	serverBroker := NewMessageBroker(stream.ServerStream(), &SimpleMessageEnvelope{}, "server", nil)

	// Register a handler that panics
	panicHandler := func(ctx context.Context, req *TestRequest) (*TestMessage, error) {
		panic("intentional panic for testing")
	}

	err := serverBroker.On(panicHandler)
	require.NoError(t, err, "Handler registration should succeed")

	// Start both brokers
	go serverBroker.Run(ctx)
	go clientBroker.Run(ctx)

	// Wait for both brokers' Run() loops to enter the receive loop.
	require.NoError(t, serverBroker.Ready(ctx))
	require.NoError(t, clientBroker.Ready(ctx))

	// Send request from client
	requestMsg := &TestMessage{
		RequestId: "panic-test-123",
		InnerMsg:  &TestRequest{Value: "trigger panic"},
	}

	// Client should receive an error response, not hang forever
	resp, err := clientBroker.SendAndWait(ctx, requestMsg)

	// The handler panicked, so we should get an error
	require.Error(t, err, "Client should receive error from panicked handler")
	assert.Contains(t, err.Error(), "handler panicked", "Error should indicate handler panic")
	assert.Nil(t, resp, "Response should be nil when error occurs")

	// Verify the broker is still functioning (not crashed)
	// Use a different request type to register a new handler
	type AnotherRequest struct {
		Data string
	}

	anotherHandler := func(ctx context.Context, req *AnotherRequest) (*TestMessage, error) {
		return &TestMessage{
			InnerMsg: &TestResponse{Result: "recovered"},
		}, nil
	}

	err = serverBroker.On(anotherHandler)
	require.NoError(t, err)

	// Send another request to verify broker still works
	requestMsg2 := &TestMessage{
		RequestId: "recovery-test-456",
		InnerMsg:  &AnotherRequest{Data: "test"},
	}

	resp2, err2 := clientBroker.SendAndWait(ctx, requestMsg2)
	require.NoError(t, err2, "Broker should still work after handler panic")
	require.NotNil(t, resp2, "Should receive response")

	innerResp := resp2.InnerMsg.(*TestResponse)
	assert.Equal(t, "recovered", innerResp.Result, "Should receive correct response from new handler")
}

// TestReady_BlocksUntilRunStarts verifies that Ready() blocks until Run() is called
func TestReady_BlocksUntilRunStarts(t *testing.T) {
	t.Parallel()

	stream := NewSimulatedBidiStream()
	defer stream.Close()
	envelope := &SimpleMessageEnvelope{}
	broker := NewMessageBroker(stream.ClientStream(), envelope, "client", nil)

	readyDone := make(chan error, 1)

	// Use a short-lived context just for the blocking check
	blockCtx, blockCancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer blockCancel()

	// Start Ready() with the short context - should block and then timeout
	go func() {
		readyDone <- broker.Ready(blockCtx)
	}()

	// Should timeout because Ready() blocks until Run() starts
	select {
	case err := <-readyDone:
		if err == nil {
			t.Fatal("Ready() should have blocked but returned nil")
		}
		// Expected - context deadline exceeded because Run() hasn't started
		assert.ErrorIs(t, err, context.DeadlineExceeded, "Ready() should timeout before Run() starts")
	case <-time.After(1 * time.Second):
		t.Fatal("Ready() goroutine didn't return after its context expired")
	}

	// Now use a generous context for the second Ready() call
	readyCtx, readyCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer readyCancel()

	readyDone2 := make(chan error, 1)
	go func() {
		readyDone2 <- broker.Ready(readyCtx)
	}()

	// Start Run()
	runCtx := t.Context()

	go func() {
		_ = broker.Run(runCtx)
	}()

	// Ready() should complete quickly after Run() starts
	select {
	case err := <-readyDone2:
		assert.NoError(t, err, "Ready() should complete after Run() starts")
	case <-time.After(5 * time.Second):
		t.Fatal("Ready() should have completed after Run() started")
	}
}

// TestReady_CompletesImmediatelyAfterRunStarts verifies Ready() is immediate after Run() starts
func TestReady_CompletesImmediatelyAfterRunStarts(t *testing.T) {
	t.Parallel()

	stream := NewSimulatedBidiStream()
	defer stream.Close()
	envelope := &SimpleMessageEnvelope{}
	broker := NewMessageBroker(stream.ClientStream(), envelope, "client", nil)

	// Start Run() first
	runCtx := t.Context()

	go func() {
		_ = broker.Run(runCtx)
	}()

	// Wait for Run() to reach the receive loop (closes readyCh as its first action).
	require.NoError(t, broker.Ready(runCtx))

	// Subsequent Ready() should complete immediately since readyCh is already closed.
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := broker.Ready(ctx)
	duration := time.Since(start)

	assert.NoError(t, err, "Ready() should complete successfully")
	assert.Less(t, duration, 50*time.Millisecond, "Ready() should be immediate when Run() is running")
}

// TestReady_MultipleCallersAllComplete verifies all waiters are unblocked when Run() starts
func TestReady_MultipleCallersAllComplete(t *testing.T) {
	t.Parallel()

	stream := NewSimulatedBidiStream()
	defer stream.Close()
	envelope := &SimpleMessageEnvelope{}
	broker := NewMessageBroker(stream.ClientStream(), envelope, "client", nil)

	const numCallers = 5
	readyResults := make(chan error, numCallers)
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	// Start multiple Ready() calls
	for range numCallers {
		go func() {
			readyResults <- broker.Ready(ctx)
		}()
	}

	time.Sleep(20 * time.Millisecond) // Let them block

	// Start Run()
	runCtx := t.Context()

	go func() {
		_ = broker.Run(runCtx)
	}()

	// All Ready() calls should complete
	for i := range numCallers {
		select {
		case err := <-readyResults:
			assert.NoError(t, err, "Ready() call %d should complete successfully", i)
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("Ready() call %d did not complete in time", i)
		}
	}
}

// TestReady_ContextCancellation verifies Ready() respects context cancellation
func TestReady_ContextCancellation(t *testing.T) {
	t.Parallel()

	stream := NewSimulatedBidiStream()
	defer stream.Close()
	envelope := &SimpleMessageEnvelope{}
	broker := NewMessageBroker(stream.ClientStream(), envelope, "client", nil)

	ctx, cancel := context.WithCancel(t.Context())

	readyDone := make(chan error, 1)
	go func() {
		readyDone <- broker.Ready(ctx)
	}()

	// The Ready() contract guarantees ctx.Err() is returned when ctx is cancelled
	// before Run() starts. No need to prove the goroutine is blocked first — a
	// closed context causes Ready() to return regardless of scheduling order.
	cancel()

	// Ready() should return with context cancellation error
	select {
	case err := <-readyDone:
		assert.Error(t, err, "Ready() should return error when context is cancelled")
		assert.Contains(t, err.Error(), "context canceled", "Should be context cancellation error")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Ready() should have returned after context cancellation")
	}
}

// TestReady_RunAlreadyStartedMultipleTimes verifies Ready() is always immediate after Run()
func TestReady_RunAlreadyStartedMultipleTimes(t *testing.T) {
	t.Parallel()

	stream := NewSimulatedBidiStream()
	defer stream.Close()
	envelope := &SimpleMessageEnvelope{}
	broker := NewMessageBroker(stream.ClientStream(), envelope, "client", nil)

	// Start Run()
	runCtx := t.Context()

	go func() {
		_ = broker.Run(runCtx)
	}()

	// Wait for Run() to reach the receive loop (closes readyCh as its first action).
	require.NoError(t, broker.Ready(runCtx))

	// Multiple Ready() calls should all be immediate
	ctx := t.Context()
	for i := range 3 {
		start := time.Now()
		err := broker.Ready(ctx)
		duration := time.Since(start)

		assert.NoError(t, err, "Ready() call %d should succeed", i)
		assert.Less(t, duration, 10*time.Millisecond, "Ready() call %d should be immediate", i)
	}
}

func TestWrapResourceExhausted_NilError(t *testing.T) {
	result := wrapResourceExhausted(nil, "test-op")
	require.Nil(t, result)
}

func TestWrapResourceExhausted_NonGRPCError(t *testing.T) {
	origErr := errors.New("some random error")
	result := wrapResourceExhausted(origErr, "test-op")
	require.Equal(t, origErr, result, "non-gRPC errors should pass through unchanged")
}

func TestWrapResourceExhausted_ResourceExhaustedError(t *testing.T) {
	grpcErr := status.Error(codes.ResourceExhausted, "message too large")
	result := wrapResourceExhausted(grpcErr, "Send chunk")

	require.Error(t, result)
	require.True(t, errors.Is(result, ErrResourceExhausted),
		"should wrap with ErrResourceExhausted sentinel")
	require.ErrorContains(t, result, "Send chunk failed")
	require.ErrorContains(t, result, "4 MB max")
	require.ErrorContains(t, result, "grpc.UseCompressor")
}

func TestWrapResourceExhausted_OtherGRPCCode(t *testing.T) {
	grpcErr := status.Error(codes.NotFound, "not found")
	result := wrapResourceExhausted(grpcErr, "test-op")
	require.Equal(t, grpcErr, result, "non-ResourceExhausted gRPC errors should pass through")
}

func TestInvokeHandler_NilNilSuppressesResponse(t *testing.T) {
	sim := NewSimulatedBidiStream()
	defer sim.Close()

	envelope := &SimpleMessageEnvelope{}
	broker := NewMessageBroker(sim.ServerStream(), envelope, "test", nil)

	// Register handler that returns (nil, nil)
	err := broker.On(func(_ context.Context, _ *TestRequest) (*TestMessage, error) {
		return nil, nil
	})
	require.NoError(t, err)

	// Simulate dispatching via processMessage
	msg := &TestMessage{
		RequestId: "req-nil-nil",
		InnerMsg:  &TestRequest{Value: "trigger"},
	}

	requestType := reflect.TypeFor[*TestRequest]()
	wrapper, ok := broker.handlers.Load(requestType)
	require.True(t, ok)
	responseEnvelope := broker.invokeHandler(
		t.Context(),
		wrapper,
		msg,
		&TestRequest{Value: "trigger"},
	)
	require.Nil(t, responseEnvelope,
		"invokeHandler should return nil when handler returns (nil, nil)")
}

func TestInvokeHandler_NilEnvelopeWithError(t *testing.T) {
	sim := NewSimulatedBidiStream()
	defer sim.Close()

	envelope := &SimpleMessageEnvelope{}
	broker := NewMessageBroker(sim.ServerStream(), envelope, "test", nil)

	// Register handler that returns (nil, error)
	err := broker.On(func(_ context.Context, _ *TestRequest) (*TestMessage, error) {
		return nil, errors.New("check failed")
	})
	require.NoError(t, err)

	requestType := reflect.TypeFor[*TestRequest]()
	wrapper, ok := broker.handlers.Load(requestType)
	require.True(t, ok)
	responseEnvelope := broker.invokeHandler(
		t.Context(),
		wrapper,
		&TestMessage{RequestId: "req-err", InnerMsg: &TestRequest{Value: "x"}},
		&TestRequest{Value: "x"},
	)
	require.NotNil(t, responseEnvelope,
		"invokeHandler should create envelope when handler returns error")
	require.Error(t, responseEnvelope.Error)
	require.ErrorContains(t, responseEnvelope.Error, "check failed")
}

func TestProcessHandlerRequest_NilNilHandler_NoSend(t *testing.T) {
	sim := NewSimulatedBidiStream()
	defer sim.Close()

	envelope := &SimpleMessageEnvelope{}
	broker := NewMessageBroker(sim.ServerStream(), envelope, "test", nil)

	// Register handler that returns (nil, nil) — should NOT trigger a Send
	err := broker.On(func(_ context.Context, _ *TestRequest) (*TestMessage, error) {
		return nil, nil
	})
	require.NoError(t, err)

	// Call processHandlerRequest directly
	msg := &TestMessage{
		RequestId: "req-suppress",
		InnerMsg:  &TestRequest{Value: "data"},
	}
	requestType := reflect.TypeFor[*TestRequest]()
	broker.processHandlerRequest(t.Context(), msg, "req-suppress", requestType)

	// Verify nothing was sent to the client
	select {
	case resp := <-sim.serverToClient:
		t.Fatalf("expected no response, but got: %+v", resp)
	default:
		// Good — nothing was sent
	}
}

func TestProcessHandlerRequest_CanceledNilHandler_SendsCancellation(t *testing.T) {
	sim := NewSimulatedBidiStream()
	defer sim.Close()

	envelope := &SimpleMessageEnvelope{}
	broker := NewMessageBroker(sim.ServerStream(), envelope, "test", nil)
	require.NoError(t, broker.On(func(_ context.Context, _ *TestRequest) (*TestMessage, error) {
		return nil, nil
	}))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	broker.processHandlerRequest(ctx, &TestMessage{
		RequestId: "req-canceled",
		InnerMsg:  &TestRequest{Value: "data"},
	}, "req-canceled", reflect.TypeFor[*TestRequest]())

	select {
	case resp := <-sim.serverToClient:
		require.Equal(t, "req-canceled", resp.RequestId)
		require.ErrorIs(t, resp.Error, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("canceled handler did not send a response")
	}
}

func TestProcessHandlerRequest_DeadlineContextErrorHandler_PreservesCause(t *testing.T) {
	sim := NewSimulatedBidiStream()
	defer sim.Close()

	envelope := &SimpleMessageEnvelope{}
	broker := NewMessageBroker(sim.ServerStream(), envelope, "test", nil)
	require.NoError(t, broker.On(func(ctx context.Context, _ *TestRequest) (*TestMessage, error) {
		return nil, ctx.Err()
	}))

	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(context.DeadlineExceeded)
	broker.processHandlerRequest(ctx, &TestMessage{
		RequestId: "req-deadline",
		InnerMsg:  &TestRequest{Value: "data"},
	}, "req-deadline", reflect.TypeFor[*TestRequest]())

	select {
	case resp := <-sim.serverToClient:
		require.Equal(t, "req-deadline", resp.RequestId)
		require.ErrorIs(t, resp.Error, context.DeadlineExceeded)
	case <-time.After(time.Second):
		t.Fatal("timed-out handler did not send a response")
	}
}

func TestProcessHandlerRequest_NoHandler_Drops(t *testing.T) {
	sim := NewSimulatedBidiStream()
	defer sim.Close()

	envelope := &SimpleMessageEnvelope{}
	broker := NewMessageBroker(sim.ServerStream(), envelope, "test", nil)

	// No handler registered for TestRequest
	msg := &TestMessage{
		RequestId: "req-nohandler",
		InnerMsg:  &TestRequest{Value: "data"},
	}
	// Use a type that has no handler
	requestType := reflect.TypeFor[*TestResponse]()
	broker.processHandlerRequest(t.Context(), msg, "req-nohandler", requestType)

	// Verify nothing was sent
	select {
	case resp := <-sim.serverToClient:
		t.Fatalf("expected no response, but got: %+v", resp)
	default:
		// Good — dropped as expected
	}
}

func TestProcessHandlerRequest_NilInnerMsg(t *testing.T) {
	sim := NewSimulatedBidiStream()
	defer sim.Close()

	envelope := &SimpleMessageEnvelope{}
	broker := NewMessageBroker(sim.ServerStream(), envelope, "test", nil)

	// Message with nil InnerMsg — GetInnerMessage returns nil for nil msg.InnerMsg
	// But SimpleMessageEnvelope falls back to TestRequest from Data field.
	// To truly get nil, pass nil message:
	msg := (*TestMessage)(nil)
	requestType := reflect.TypeFor[*TestRequest]()
	broker.processHandlerRequest(t.Context(), msg, "req-nil", requestType)

	// Verify nothing was sent
	select {
	case resp := <-sim.serverToClient:
		t.Fatalf("expected no response, but got: %+v", resp)
	default:
		// Good — nil inner message path
	}
}
