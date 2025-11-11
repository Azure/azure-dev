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
	closed         bool
	mu             sync.Mutex
}

func NewSimulatedBidiStream() *SimulatedBidiStream {
	return &SimulatedBidiStream{
		clientToServer: make(chan *TestMessage, 10),
		serverToClient: make(chan *TestMessage, 10),
	}
}

func (s *SimulatedBidiStream) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.clientToServer)
		close(s.serverToClient)
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
	c.sim.clientToServer <- msg
	return nil
}

func (c *clientSideStream) Recv() (*TestMessage, error) {
	msg, ok := <-c.sim.serverToClient
	if !ok {
		return nil, io.EOF
	}
	return msg, nil
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
	s.sim.serverToClient <- msg
	return nil
}

func (s *serverSideStream) Recv() (*TestMessage, error) {
	msg, ok := <-s.sim.clientToServer
	if !ok {
		return nil, io.EOF
	}
	return msg, nil
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

// TestOn_RegistersHandler tests that handlers are registered correctly
func TestOn_RegistersHandler(t *testing.T) {
	sim := NewSimulatedBidiStream()
	defer sim.Close()

	envelope := &SimpleMessageEnvelope{}
	broker := NewMessageBroker(sim.ServerStream(), envelope, "test")

	// Valid handler with context and request only
	handler := func(ctx context.Context, req *TestRequest) (*TestMessage, error) {
		return &TestMessage{Data: req.Value}, nil
	}

	err := broker.On(handler)
	require.NoError(t, err)

	// Verify handler was registered
	requestType := reflect.TypeOf((*TestRequest)(nil))
	_, ok := broker.handlers.Load(requestType)
	assert.True(t, ok, "Handler should be registered")
}

// TestOn_RegistersHandlerWithProgress tests that handlers with progress callback are registered correctly
func TestOn_RegistersHandlerWithProgress(t *testing.T) {
	sim := NewSimulatedBidiStream()
	defer sim.Close()

	envelope := &SimpleMessageEnvelope{}
	broker := NewMessageBroker(sim.ServerStream(), envelope, "test")

	// Valid handler with progress callback
	handler := func(ctx context.Context, req *TestRequest, progress ProgressFunc) (*TestMessage, error) {
		progress("working...")
		return &TestMessage{Data: req.Value}, nil
	}

	err := broker.On(handler)
	require.NoError(t, err)

	// Verify handler was registered with progress flag
	requestType := reflect.TypeOf((*TestRequest)(nil))
	wrapper, ok := broker.handlers.Load(requestType)
	require.True(t, ok, "Handler should be registered")

	handlerWrapper := wrapper.(*handlerWrapper)
	assert.True(t, handlerWrapper.hasProgress, "Handler should be marked as having progress")
	assert.Equal(t, 2, handlerWrapper.progressIndex, "Progress parameter should be at index 2")
}

// TestOn_InvalidHandler tests validation of invalid handler signatures
func TestOn_InvalidHandler(t *testing.T) {
	sim := NewSimulatedBidiStream()
	defer sim.Close()

	envelope := &SimpleMessageEnvelope{}
	broker := NewMessageBroker(sim.ServerStream(), envelope, "test")

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
	clientBroker := NewMessageBroker(sim.ClientStream(), envelope, "client")

	ctx := context.Background()
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
	clientBroker := NewMessageBroker(sim.ClientStream(), envelope, "client")

	ctx := context.Background()
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
	clientBroker := NewMessageBroker(sim.ClientStream(), envelope, "client")
	serverBroker := NewMessageBroker(sim.ServerStream(), envelope, "server")

	ctx, cancel := context.WithCancel(context.Background())
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

	// Give brokers time to start
	time.Sleep(50 * time.Millisecond)

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
	clientBroker := NewMessageBroker(sim.ClientStream(), envelope, "client")
	serverBroker := NewMessageBroker(sim.ServerStream(), envelope, "server")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Register handler with progress
	handler := func(ctx context.Context, req *TestRequest, progress ProgressFunc) (*TestMessage, error) {
		progress("Starting...")
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

	time.Sleep(50 * time.Millisecond)

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
	clientBroker := NewMessageBroker(sim.ClientStream(), envelope, "client")
	serverBroker := NewMessageBroker(sim.ServerStream(), envelope, "server")

	ctx, cancel := context.WithCancel(context.Background())
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

	time.Sleep(50 * time.Millisecond)

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
	serverBroker := NewMessageBroker(sim.ServerStream(), envelope, "server")
	clientBroker := NewMessageBroker(sim.ClientStream(), envelope, "client")

	ctx, cancel := context.WithCancel(context.Background())
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

	time.Sleep(50 * time.Millisecond)

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
	broker := NewMessageBroker(sim.ServerStream(), envelope, "test")

	ctx, cancel := context.WithCancel(context.Background())

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
	case <-time.After(1 * time.Second):
		t.Fatal("Run did not exit after context cancellation")
	}
}

// TestRun_GracefulShutdown_EOF tests EOF handling
func TestRun_GracefulShutdown_EOF(t *testing.T) {
	sim := NewSimulatedBidiStream()

	envelope := &SimpleMessageEnvelope{}
	broker := NewMessageBroker(sim.ServerStream(), envelope, "test")

	ctx := context.Background()

	// Close stream immediately to cause EOF
	sim.Close()

	// Run should exit gracefully
	err := broker.Run(ctx)
	require.NoError(t, err)
}

// TestClose_ClosesAllChannels tests that Close properly cleans up
func TestClose_ClosesAllChannels(t *testing.T) {
	sim := NewSimulatedBidiStream()
	defer sim.Close()

	envelope := &SimpleMessageEnvelope{}
	broker := NewMessageBroker(sim.ClientStream(), envelope, "test")

	ctx := context.Background()

	// Start some SendAndWait operations that will register channels
	go func() {
		msg := &TestMessage{RequestId: "req1", InnerMsg: &TestRequest{Value: "test"}}
		broker.SendAndWait(ctx, msg)
	}()

	go func() {
		msg := &TestMessage{RequestId: "req2", InnerMsg: &TestRequest{Value: "test"}}
		broker.SendAndWait(ctx, msg)
	}()

	// Give time for channels to register
	time.Sleep(100 * time.Millisecond)

	// Close the broker
	broker.Close()

	// Verify all channels are removed
	count := 0
	broker.responseChans.Range(func(key, value any) bool {
		count++
		return true
	})
	assert.Equal(t, 0, count, "All channels should be removed from the map")
}

// TestEndToEnd_HandlerPanic verifies that when a handler panics, the client receives
// an error response instead of hanging forever
func TestEndToEnd_HandlerPanic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create simulated stream and both client/server brokers
	stream := NewSimulatedBidiStream()
	clientBroker := NewMessageBroker(stream.ClientStream(), &SimpleMessageEnvelope{}, "client")
	serverBroker := NewMessageBroker(stream.ServerStream(), &SimpleMessageEnvelope{}, "server")

	// Register a handler that panics
	panicHandler := func(ctx context.Context, req *TestRequest) (*TestMessage, error) {
		panic("intentional panic for testing")
	}

	err := serverBroker.On(panicHandler)
	require.NoError(t, err, "Handler registration should succeed")

	// Start both brokers
	go serverBroker.Run(ctx)
	go clientBroker.Run(ctx)

	// Give brokers time to start
	time.Sleep(50 * time.Millisecond)

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
	broker := NewMessageBroker(stream.ClientStream(), envelope, "client")

	readyDone := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Start Ready() - should block
	go func() {
		readyDone <- broker.Ready(ctx)
	}()

	// Should timeout because Ready() blocks until Run() starts
	select {
	case err := <-readyDone:
		t.Fatalf("Ready() should have blocked but returned with: %v", err)
	case <-time.After(50 * time.Millisecond):
		// Expected - Ready() is blocking
	}

	// Start Run()
	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()

	go func() {
		_ = broker.Run(runCtx)
	}()

	// Ready() should complete quickly
	select {
	case err := <-readyDone:
		assert.NoError(t, err, "Ready() should complete after Run() starts")
	case <-time.After(50 * time.Millisecond):
		t.Fatal("Ready() should have completed after Run() started")
	}
}

// TestReady_CompletesImmediatelyAfterRunStarts verifies Ready() is immediate after Run() starts
func TestReady_CompletesImmediatelyAfterRunStarts(t *testing.T) {
	t.Parallel()

	stream := NewSimulatedBidiStream()
	defer stream.Close()
	envelope := &SimpleMessageEnvelope{}
	broker := NewMessageBroker(stream.ClientStream(), envelope, "client")

	// Start Run() first
	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()

	go func() {
		_ = broker.Run(runCtx)
	}()

	time.Sleep(10 * time.Millisecond) // Let Run() start

	// Ready() should complete immediately
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
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
	broker := NewMessageBroker(stream.ClientStream(), envelope, "client")

	const numCallers = 5
	readyResults := make(chan error, numCallers)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Start multiple Ready() calls
	for i := 0; i < numCallers; i++ {
		go func() {
			readyResults <- broker.Ready(ctx)
		}()
	}

	time.Sleep(20 * time.Millisecond) // Let them block

	// Start Run()
	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()

	go func() {
		_ = broker.Run(runCtx)
	}()

	// All Ready() calls should complete
	for i := 0; i < numCallers; i++ {
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
	broker := NewMessageBroker(stream.ClientStream(), envelope, "client")

	ctx, cancel := context.WithCancel(context.Background())

	readyDone := make(chan error, 1)
	go func() {
		readyDone <- broker.Ready(ctx)
	}()

	time.Sleep(20 * time.Millisecond) // Let Ready() block

	cancel() // Cancel context

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
	broker := NewMessageBroker(stream.ClientStream(), envelope, "client")

	// Start Run()
	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()

	go func() {
		_ = broker.Run(runCtx)
	}()

	time.Sleep(10 * time.Millisecond) // Let Run() start

	// Multiple Ready() calls should all be immediate
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		start := time.Now()
		err := broker.Ready(ctx)
		duration := time.Since(start)

		assert.NoError(t, err, "Ready() call %d should succeed", i)
		assert.Less(t, duration, 10*time.Millisecond, "Ready() call %d should be immediate", i)
	}
}
