// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

// MockBidiStreamingClient mocks the gRPC bidirectional streaming client using generics
// Req represents the request message type, Resp represents the response message type
// In most cases they're the same (e.g., both *EventMessage), but the interface allows them to differ
//
// Usage examples:
//   - For EventService: MockBidiStreamingClient[*EventMessage, *EventMessage]
//   - For FrameworkService: MockBidiStreamingClient[*FrameworkServiceMessage, *FrameworkServiceMessage]
//   - For ServiceTargetService: MockBidiStreamingClient[*ServiceTargetMessage, *ServiceTargetMessage]
//
// The generic design allows this mock to be reused across all gRPC services
// that use bidirectional streaming in the azd codebase.
type MockBidiStreamingClient[Req any, Resp any] struct {
	mock.Mock
	sentMessages     []Req
	receivedMessages []Resp
}

func (m *MockBidiStreamingClient[Req, Resp]) Send(msg Req) error {
	args := m.Called(msg)
	m.sentMessages = append(m.sentMessages, msg)
	return args.Error(0)
}

func (m *MockBidiStreamingClient[Req, Resp]) Recv() (Resp, error) {
	args := m.Called()
	if len(args) > 0 && args.Get(0) != nil {
		m.receivedMessages = append(m.receivedMessages, args.Get(0).(Resp))
		return args.Get(0).(Resp), args.Error(1)
	}
	var zero Resp
	return zero, args.Error(1)
}

func (m *MockBidiStreamingClient[Req, Resp]) CloseSend() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockBidiStreamingClient[Req, Resp]) SendMsg(msg any) error {
	args := m.Called(msg)
	return args.Error(0)
}

func (m *MockBidiStreamingClient[Req, Resp]) RecvMsg(msg any) error {
	args := m.Called(msg)
	return args.Error(0)
}

func (m *MockBidiStreamingClient[Req, Resp]) Header() (metadata.MD, error) {
	args := m.Called()
	return args.Get(0).(metadata.MD), args.Error(1)
}

func (m *MockBidiStreamingClient[Req, Resp]) Trailer() metadata.MD {
	args := m.Called()
	return args.Get(0).(metadata.MD)
}

func (m *MockBidiStreamingClient[Req, Resp]) Context() context.Context {
	args := m.Called()
	return args.Get(0).(context.Context)
}

// Helper methods for tests
func (m *MockBidiStreamingClient[Req, Resp]) GetSentMessages() []Req {
	return m.sentMessages
}

func (m *MockBidiStreamingClient[Req, Resp]) GetReceivedMessages() []Resp {
	return m.receivedMessages
}

// Test helper functions
func createTestProjectConfigForEvents() *ProjectConfig {
	return &ProjectConfig{
		Name: "test-project",
		Path: "/test/path",
	}
}

func createTestServiceConfigForEvents() *ServiceConfig {
	return &ServiceConfig{
		Name: "test-service",
		Host: "containerapp",
	}
}

func createTestServiceContextForEvents() *ServiceContext {
	return &ServiceContext{
		Package: []*Artifact{
			{
				Kind:     ArtifactKind_ARTIFACT_KIND_CONTAINER,
				Location: "/test/package/path",
				Metadata: map[string]string{
					"name":     "test-package",
					"language": "go",
				},
			},
		},
	}
}

// Test EventManager creation
func TestNewEventManager(t *testing.T) {
	// Create a real AzdClient (without connection)
	client := &AzdClient{}

	eventManager := NewEventManager("microsoft.azd.demo", client, nil)

	assert.NotNil(t, eventManager)
	assert.Equal(t, client, eventManager.client)
	assert.NotNil(t, eventManager.projectEvents)
	assert.NotNil(t, eventManager.serviceEvents)
	assert.Empty(t, eventManager.projectEvents)
	assert.Empty(t, eventManager.serviceEvents)
}

// Test onInvokeProjectHandler with successful handler
func TestEventManager_onInvokeProjectHandler_Success(t *testing.T) {
	ctx := t.Context()
	client := &AzdClient{}

	eventManager := NewEventManager("microsoft.azd.demo", client, nil)

	// Add a test handler
	handlerCalled := false
	var receivedArgs *ProjectEventArgs
	handler := func(ctx context.Context, args *ProjectEventArgs) error {
		handlerCalled = true
		receivedArgs = args
		return nil
	}
	eventManager.projectEvents["prerestore"] = handler

	// Create invoke message
	invokeMsg := &InvokeProjectHandler{
		EventName: "prerestore",
		Project:   createTestProjectConfigForEvents(),
	}

	// Invoke the handler
	resp, err := eventManager.onInvokeProjectHandler(ctx, invokeMsg)

	assert.NoError(t, err)
	assert.True(t, handlerCalled)
	assert.NotNil(t, receivedArgs)
	assert.Equal(t, "test-project", receivedArgs.Project.Name)

	// Verify the response message
	require.NotNil(t, resp)
	status := resp.GetProjectHandlerStatus()
	require.NotNil(t, status)
	assert.Equal(t, "prerestore", status.EventName)
	assert.Equal(t, "completed", status.Status)
	assert.Equal(t, "", status.Message)
}

type followUpRecorder struct {
	v1beta.UnimplementedFollowUpServiceServer
	invocationID string
	text         string
}

func (r *followUpRecorder) SetFollowUp(
	ctx context.Context,
	req *v1beta.SetFollowUpRequest,
) (*v1beta.SetFollowUpResponse, error) {
	r.invocationID = req.InvocationId
	r.text = req.Text
	return &v1beta.SetFollowUpResponse{}, nil
}

type betaEventStreamRecorder struct {
	v1beta.UnimplementedEventServiceServer
	started       chan struct{}
	subscriptions chan *v1beta.EventMessage
}

type controlledBetaEventStream struct {
	ctx       context.Context
	requests  chan *v1beta.EventMessage
	responses chan *v1beta.EventMessage
}

func (s *controlledBetaEventStream) Send(msg *v1beta.EventMessage) error {
	select {
	case s.requests <- msg:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

func (s *controlledBetaEventStream) Recv() (*v1beta.EventMessage, error) {
	select {
	case response := <-s.responses:
		return response, nil
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

func newControlledPreviewEventManager(
	t *testing.T,
) (*previewEventManager, *controlledBetaEventStream) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	stream := &controlledBetaEventStream{
		ctx:       ctx,
		requests:  make(chan *v1beta.EventMessage, 8),
		responses: make(chan *v1beta.EventMessage, 8),
	}
	manager := newPreviewEventManager("test-ext", &AzdClient{}, nil)
	manager.broker = grpcbroker.NewMessageBroker(
		stream,
		newBetaEventMessageEnvelope(),
		"test-ext",
		nil,
	)

	receiveDone := make(chan error, 1)
	go func() {
		receiveDone <- manager.Receive(ctx)
	}()
	require.NoError(t, manager.Ready(ctx))
	t.Cleanup(func() {
		cancel()
		select {
		case <-receiveDone:
		case <-time.After(time.Second):
			t.Error("preview event manager receive loop did not stop")
		}
	})

	return manager, stream
}

func receiveControlledSubscription(
	t *testing.T,
	stream *controlledBetaEventStream,
) *v1beta.EventMessage {
	t.Helper()
	select {
	case message := <-stream.requests:
		require.NotNil(t, message.GetSubscribeProjectEvent())
		require.NotEmpty(t, message.GetRequestId())
		return message
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for preview event subscription")
		return nil
	}
}

func sendControlledSubscriptionResponse(
	stream *controlledBetaEventStream,
	request *v1beta.EventMessage,
) {
	stream.responses <- &v1beta.EventMessage{
		RequestId: request.GetRequestId(),
		MessageType: &v1beta.EventMessage_SubscribeProjectEventResponse{
			SubscribeProjectEventResponse: &v1beta.SubscribeProjectEventResponse{},
		},
	}
}

func (r *betaEventStreamRecorder) EventStream(
	stream grpc.BidiStreamingServer[v1beta.EventMessage, v1beta.EventMessage],
) error {
	close(r.started)
	for {
		message, err := stream.Recv()
		if err != nil {
			return err
		}
		r.subscriptions <- message
		if message.GetSubscribeProjectEvent() != nil {
			if err := stream.Send(&v1beta.EventMessage{
				RequestId: message.RequestId,
				MessageType: &v1beta.EventMessage_SubscribeProjectEventResponse{
					SubscribeProjectEventResponse: &v1beta.SubscribeProjectEventResponse{},
				},
			}); err != nil {
				return err
			}
		}
	}
}

func TestPreviewEventManager_ConcurrentRegistration(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	recorder := &betaEventStreamRecorder{
		started:       make(chan struct{}),
		subscriptions: make(chan *v1beta.EventMessage, 32),
	}
	v1beta.RegisterEventServiceServer(server, recorder)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	connection, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = connection.Close()
	})

	manager := newPreviewEventManager("test-ext", &AzdClient{connection: connection}, nil)
	ctx, cancel := context.WithCancel(t.Context())
	receiveDone := make(chan error, 1)
	go func() {
		receiveDone <- manager.Receive(ctx)
	}()
	<-recorder.started

	const handlerCount = 16
	var wg sync.WaitGroup
	errs := make(chan error, handlerCount)
	for i := range handlerCount {
		eventName := fmt.Sprintf("event-%d", i)
		wg.Go(func() {
			errs <- manager.AddProjectEventHandler(ctx, eventName, func(
				context.Context,
				*PreviewProjectEventArgs,
			) error {
				return nil
			})
		})
	}
	wg.Wait()
	close(errs)
	for registrationErr := range errs {
		require.NoError(t, registrationErr)
	}

	for range handlerCount {
		select {
		case message := <-recorder.subscriptions:
			require.NotNil(t, message.GetSubscribeProjectEvent())
		case <-ctx.Done():
			t.Fatal("context canceled before all subscriptions were received")
		}
	}

	require.NoError(t, manager.Close())
	cancel()
	<-receiveDone
}

func TestPreviewEventManager_DistinctRegistrationsAwaitAcknowledgementsIndependently(t *testing.T) {
	oldTimeout := previewEventRegistrationTimeout
	previewEventRegistrationTimeout = 10 * time.Second
	t.Cleanup(func() {
		previewEventRegistrationTimeout = oldTimeout
	})

	manager, stream := newControlledPreviewEventManager(t)

	firstCtx, cancelFirst := context.WithCancel(t.Context())
	defer cancelFirst()
	firstResult := make(chan error, 1)
	go func() {
		firstResult <- manager.AddProjectEventHandler(
			firstCtx,
			"postrestore",
			func(context.Context, *PreviewProjectEventArgs) error {
				return nil
			},
		)
	}()
	firstRequest := receiveControlledSubscription(t, stream)

	secondCtx, cancelSecond := context.WithCancel(t.Context())
	defer cancelSecond()
	secondResult := make(chan error, 1)
	go func() {
		secondResult <- manager.AddProjectEventHandler(
			secondCtx,
			"postbuild",
			func(context.Context, *PreviewProjectEventArgs) error {
				return nil
			},
		)
	}()

	var secondRequest *v1beta.EventMessage
	select {
	case secondRequest = <-stream.requests:
		require.NotNil(t, secondRequest.GetSubscribeProjectEvent())
	case <-time.After(2 * time.Second):
		t.Fatal("second subscription was blocked by the first acknowledgement")
	}
	require.NotEqual(t, firstRequest.GetRequestId(), secondRequest.GetRequestId())

	sendControlledSubscriptionResponse(stream, secondRequest)
	select {
	case err := <-secondResult:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("second subscription did not complete after its acknowledgement")
	}

	sendControlledSubscriptionResponse(stream, firstRequest)
	select {
	case err := <-firstResult:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("first subscription did not complete after its acknowledgement")
	}
}

func TestPreviewEventManager_RegistrationLocksArePerEvent(t *testing.T) {
	manager := newPreviewEventManager("test-ext", &AzdClient{}, nil)

	first := manager.eventRegistrationLock("postdeploy")
	require.Same(t, first, manager.eventRegistrationLock("postdeploy"))
	require.NotSame(t, first, manager.eventRegistrationLock("postbuild"))
}

func TestPreviewEventManager_RegistrationFailureRollsBackHandler(t *testing.T) {
	expectedErr := errors.New("send failed")

	tests := []struct {
		name        string
		withCurrent bool
	}{
		{name: "new handler"},
		{name: "replacement handler", withCurrent: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := &MockBidiStreamingClient[
				*v1beta.EventMessage,
				*v1beta.EventMessage,
			]{}
			stream.On("Send", mock.Anything).Return(expectedErr).Once()

			manager := newPreviewEventManager("test-ext", &AzdClient{}, nil)
			manager.broker = grpcbroker.NewMessageBroker(
				stream,
				newBetaEventMessageEnvelope(),
				"test-ext",
				nil,
			)

			currentCalled := false
			if tt.withCurrent {
				manager.handlers["postdeploy"] = func(
					context.Context,
					*PreviewProjectEventArgs,
				) error {
					currentCalled = true
					return nil
				}
			}

			replacementCalled := false
			err := manager.AddProjectEventHandler(
				t.Context(),
				"postdeploy",
				func(context.Context, *PreviewProjectEventArgs) error {
					replacementCalled = true
					return nil
				},
			)
			require.ErrorIs(t, err, expectedErr)

			response, err := manager.onInvokeProjectHandler(
				t.Context(),
				&v1beta.InvokeProjectHandler{EventName: "postdeploy"},
			)
			require.NoError(t, err)
			require.False(t, replacementCalled)
			if tt.withCurrent {
				require.True(t, currentCalled)
				require.NotNil(t, response.GetProjectHandlerStatus())
			} else {
				require.False(t, currentCalled)
				require.Nil(t, response.MessageType)
			}
			stream.AssertExpectations(t)
		})
	}
}

func TestPreviewEventManager_RegistrationTimeoutPreservesReason(t *testing.T) {
	oldTimeout := previewEventRegistrationTimeout
	previewEventRegistrationTimeout = 100 * time.Millisecond
	t.Cleanup(func() {
		previewEventRegistrationTimeout = oldTimeout
	})

	sendStarted := make(chan struct{})
	stream := &MockBidiStreamingClient[
		*v1beta.EventMessage,
		*v1beta.EventMessage,
	]{}
	stream.On("Send", mock.Anything).Run(func(mock.Arguments) {
		close(sendStarted)
	}).Return(nil).Once()

	manager := newPreviewEventManager("test-ext", &AzdClient{}, nil)
	manager.broker = grpcbroker.NewMessageBroker(
		stream,
		newBetaEventMessageEnvelope(),
		"test-ext",
		nil,
	)

	result := make(chan error, 1)
	go func() {
		result <- manager.AddProjectEventHandler(
			t.Context(),
			"postdeploy",
			func(context.Context, *PreviewProjectEventArgs) error {
				return nil
			},
		)
	}()
	select {
	case <-sendStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("preview event subscription was not sent")
	}
	var err error
	select {
	case err = <-result:
	case <-time.After(5 * time.Second):
		t.Fatal("preview event subscription did not time out")
	}
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "acknowledgement timed out")
	require.NotContains(t, err.Error(), "not supported")
	_, exists := manager.handlers["postdeploy"]
	require.False(t, exists)
	stream.AssertExpectations(t)
}

func TestPreviewEventManager_RegistrationCancellationPreservesReason(t *testing.T) {
	sendStarted := make(chan struct{})
	stream := &MockBidiStreamingClient[
		*v1beta.EventMessage,
		*v1beta.EventMessage,
	]{}
	stream.On("Send", mock.Anything).Run(func(mock.Arguments) {
		close(sendStarted)
	}).Return(nil).Once()

	manager := newPreviewEventManager("test-ext", &AzdClient{}, nil)
	manager.broker = grpcbroker.NewMessageBroker(
		stream,
		newBetaEventMessageEnvelope(),
		"test-ext",
		nil,
	)
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		result <- manager.AddProjectEventHandler(
			ctx,
			"postdeploy",
			func(context.Context, *PreviewProjectEventArgs) error {
				return nil
			},
		)
	}()

	select {
	case <-sendStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("preview event subscription was not sent")
	}
	cancel()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
		require.NotContains(t, err.Error(), "not supported")
	case <-time.After(5 * time.Second):
		t.Fatal("preview event subscription did not return after cancellation")
	}

	_, exists := manager.handlers["postdeploy"]
	require.False(t, exists)
	stream.AssertExpectations(t)
}

func TestFollowUpContributionSetAndClear(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	recorder := &followUpRecorder{}
	v1beta.RegisterFollowUpServiceServer(server, recorder)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	connection, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = connection.Close()
	})

	t.Setenv("AZD_ACCESS_TOKEN", "test-token")
	contribution := &FollowUpContribution{
		client:       &AzdClient{connection: connection},
		ctx:          t.Context(),
		invocationID: "invocation-id",
	}

	require.NoError(t, contribution.Set("next"))
	require.Equal(t, "invocation-id", recorder.invocationID)
	require.Equal(t, "next", recorder.text)

	require.NoError(t, contribution.Clear())
	require.Equal(t, "", recorder.text)
}

// Test onInvokeProjectHandler with handler error
func TestEventManager_onInvokeProjectHandler_HandlerError(t *testing.T) {
	ctx := t.Context()
	client := &AzdClient{}

	eventManager := NewEventManager("microsoft.azd.demo", client, nil)

	// Add a test handler that fails with a structured LocalError so we can verify
	// the failure response carries the structured error payload, not just a string.
	handlerErr := &LocalError{
		Message:    "handler failed",
		Code:       "handler_failed",
		Category:   LocalErrorCategoryUser,
		Suggestion: "Try again with --debug",
	}
	handler := func(ctx context.Context, args *ProjectEventArgs) error {
		return handlerErr
	}
	eventManager.projectEvents["postbuild"] = handler

	// Create invoke message
	invokeMsg := &InvokeProjectHandler{
		EventName: "postbuild",
		Project:   createTestProjectConfigForEvents(),
	}

	// Invoke the handler
	resp, err := eventManager.onInvokeProjectHandler(ctx, invokeMsg)

	assert.NoError(t, err) // onInvokeProjectHandler doesn't return handler errors, it wraps them in the response

	// Verify the response message shows failure
	require.NotNil(t, resp)
	status := resp.GetProjectHandlerStatus()
	require.NotNil(t, status)
	assert.Equal(t, "postbuild", status.EventName)
	assert.Equal(t, "failed", status.Status)
	assert.Equal(t, "handler failed", status.Message)

	// Verify the structured ExtensionError is also populated so the host can unwrap
	// it back into a typed LocalError (preserving the Suggestion and category).
	require.NotNil(t, status.Error, "expected structured ExtensionError on failed status")
	assert.Equal(t, "handler failed", status.Error.GetMessage())
	assert.Equal(t, "Try again with --debug", status.Error.GetSuggestion())
	require.NotNil(t, status.Error.GetLocalError())
	assert.Equal(t, "handler_failed", status.Error.GetLocalError().GetCode())
	assert.Equal(t, string(LocalErrorCategoryUser), status.Error.GetLocalError().GetCategory())
}

func TestPreviewEventManager_onInvokeProjectHandler_StructuredError(t *testing.T) {
	tests := []struct {
		name       string
		handlerErr error
		assertErr  func(*testing.T, *v1beta.ExtensionError)
	}{
		{
			name: "local",
			handlerErr: &LocalError{
				Message:    "preview handler failed",
				Code:       "preview_failed",
				Category:   LocalErrorCategoryUser,
				CauseTypes: []string{"*example.Cause"},
				Suggestion: "Try again",
			},
			assertErr: func(t *testing.T, err *v1beta.ExtensionError) {
				require.Equal(t, "Try again", err.GetSuggestion())
				require.NotNil(t, err.GetLocalError())
				require.Equal(t, "preview_failed", err.GetLocalError().GetCode())
				require.Equal(t, []string{"*example.Cause"}, err.GetLocalError().GetCauseTypes())
			},
		},
		{
			name: "tool",
			handlerErr: &ToolError{
				Message:    "tool failed",
				ToolName:   "az",
				Kind:       ToolErrorKindMissing,
				ExitCode:   new(127),
				Suggestion: "Install az",
			},
			assertErr: func(t *testing.T, err *v1beta.ExtensionError) {
				require.Equal(t, "Install az", err.GetSuggestion())
				require.NotNil(t, err.GetToolError())
				require.Equal(t, "az", err.GetToolError().GetToolName())
				require.Equal(t, string(ToolErrorKindMissing), err.GetToolError().GetFailureKind())
				require.Equal(t, int64(127), err.GetToolError().GetExitCode())
			},
		},
		{
			name: "service takes precedence over wrapped tool",
			handlerErr: &ToolError{
				Message:  "tool wrapper failed",
				ToolName: "az",
				Err: &ServiceError{
					Message:     "service request failed",
					ErrorCode:   "Unavailable",
					StatusCode:  503,
					ServiceName: "example.test",
				},
			},
			assertErr: func(t *testing.T, err *v1beta.ExtensionError) {
				require.Equal(t, v1beta.ErrorOrigin_ERROR_ORIGIN_SERVICE, err.GetOrigin())
				require.Equal(t, "service request failed", err.GetMessage())
				require.NotNil(t, err.GetServiceError())
				require.Equal(t, "Unavailable", err.GetServiceError().GetErrorCode())
				require.Equal(t, int32(503), err.GetServiceError().GetStatusCode())
				require.Nil(t, err.GetToolError())
			},
		},
		{
			name: "local takes precedence over wrapped tool",
			handlerErr: &ToolError{
				Message:  "tool wrapper failed",
				ToolName: "az",
				Err: &LocalError{
					Message:    "local validation failed",
					Code:       "invalid_setting",
					Category:   LocalErrorCategoryValidation,
					CauseTypes: []string{"*example.Cause"},
				},
			},
			assertErr: func(t *testing.T, err *v1beta.ExtensionError) {
				require.Equal(t, v1beta.ErrorOrigin_ERROR_ORIGIN_LOCAL, err.GetOrigin())
				require.Equal(t, "local validation failed", err.GetMessage())
				require.NotNil(t, err.GetLocalError())
				require.Equal(t, "invalid_setting", err.GetLocalError().GetCode())
				require.Equal(t, []string{"*example.Cause"}, err.GetLocalError().GetCauseTypes())
				require.Nil(t, err.GetToolError())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := newPreviewEventManager("test-ext", &AzdClient{}, nil)
			manager.handlers["postdeploy"] = func(
				context.Context,
				*PreviewProjectEventArgs,
			) error {
				return tt.handlerErr
			}

			resp, err := manager.onInvokeProjectHandler(t.Context(), &v1beta.InvokeProjectHandler{
				EventName: "postdeploy",
				Project:   &v1beta.ProjectConfig{Name: "test-project"},
			})

			require.NoError(t, err)
			require.NotNil(t, resp)
			status := resp.GetProjectHandlerStatus()
			require.NotNil(t, status)
			require.Equal(t, "failed", status.GetStatus())
			require.NotNil(t, status.GetError())
			tt.assertErr(t, status.GetError())
		})
	}
}

// Test onInvokeProjectHandler with no registered handler
func TestEventManager_onInvokeProjectHandler_NoHandler(t *testing.T) {
	ctx := t.Context()
	client := &AzdClient{}
	eventManager := NewEventManager("microsoft.azd.demo", client, nil)

	// Create invoke message for unregistered event
	invokeMsg := &InvokeProjectHandler{
		EventName: "nonexistentevent",
		Project:   createTestProjectConfigForEvents(),
	}

	// Invoke should return an empty response (not an error, just no status message)
	resp, err := eventManager.onInvokeProjectHandler(ctx, invokeMsg)

	assert.NoError(t, err)
	assert.NotNil(t, resp)          // Returns empty EventMessage, not nil
	assert.Nil(t, resp.MessageType) // But the MessageType is nil (empty message)
}

// Test onInvokeServiceHandler with successful handler
func TestEventManager_onInvokeServiceHandler_Success(t *testing.T) {
	ctx := t.Context()
	client := &AzdClient{}

	eventManager := NewEventManager("microsoft.azd.demo", client, nil)

	// Add a test handler
	handlerCalled := false
	var receivedArgs *ServiceEventArgs
	handler := func(ctx context.Context, args *ServiceEventArgs) error {
		handlerCalled = true
		receivedArgs = args
		return nil
	}
	eventManager.serviceEvents["prepackage"] = handler

	// Create invoke message with ServiceContext
	invokeMsg := &InvokeServiceHandler{
		EventName:      "prepackage",
		Project:        createTestProjectConfigForEvents(),
		Service:        createTestServiceConfigForEvents(),
		ServiceContext: createTestServiceContextForEvents(),
	}

	// Invoke the handler
	resp, err := eventManager.onInvokeServiceHandler(ctx, invokeMsg)

	assert.NoError(t, err)
	assert.True(t, handlerCalled)
	assert.NotNil(t, receivedArgs)
	assert.Equal(t, "test-project", receivedArgs.Project.Name)
	assert.Equal(t, "test-service", receivedArgs.Service.Name)
	assert.NotNil(t, receivedArgs.ServiceContext)
	assert.NotNil(t, receivedArgs.ServiceContext.Package)
	assert.Len(t, receivedArgs.ServiceContext.Package, 1)
	assert.Equal(t, "test-package", receivedArgs.ServiceContext.Package[0].Metadata["name"])

	// Verify the response message
	require.NotNil(t, resp)
	status := resp.GetServiceHandlerStatus()
	require.NotNil(t, status)
	assert.Equal(t, "prepackage", status.EventName)
	assert.Equal(t, "test-service", status.ServiceName)
	assert.Equal(t, "completed", status.Status)
	assert.Equal(t, "", status.Message)
}

// Test onInvokeServiceHandler with nil ServiceContext (should default to empty)
func TestEventManager_onInvokeServiceHandler_NilServiceContext(t *testing.T) {
	ctx := t.Context()
	client := &AzdClient{}

	eventManager := NewEventManager("microsoft.azd.demo", client, nil)

	// Add a test handler
	var receivedArgs *ServiceEventArgs
	handler := func(ctx context.Context, args *ServiceEventArgs) error {
		receivedArgs = args
		return nil
	}
	eventManager.serviceEvents["postdeploy"] = handler

	// Create invoke message with nil ServiceContext
	invokeMsg := &InvokeServiceHandler{
		EventName:      "postdeploy",
		Project:        createTestProjectConfigForEvents(),
		Service:        createTestServiceConfigForEvents(),
		ServiceContext: nil, // nil context
	}

	// Invoke the handler
	resp, err := eventManager.onInvokeServiceHandler(ctx, invokeMsg)

	assert.NoError(t, err)
	assert.NotNil(t, receivedArgs)
	assert.NotNil(t, receivedArgs.ServiceContext) // Should be defaulted to empty instance

	// Verify the response message
	require.NotNil(t, resp)
	status := resp.GetServiceHandlerStatus()
	require.NotNil(t, status)
	assert.Equal(t, "postdeploy", status.EventName)
	assert.Equal(t, "test-service", status.ServiceName)
	assert.Equal(t, "completed", status.Status)
}

// Test onInvokeServiceHandler with handler error
func TestEventManager_onInvokeServiceHandler_HandlerError(t *testing.T) {
	ctx := t.Context()
	client := &AzdClient{}

	eventManager := NewEventManager("microsoft.azd.demo", client, nil)

	// Add a test handler that fails with a structured LocalError so we can verify
	// the failure response carries the structured error payload, not just a string.
	handlerErr := &LocalError{
		Message:    "service handler failed",
		Code:       "service_handler_failed",
		Category:   LocalErrorCategoryUser,
		Suggestion: "Re-run with --debug",
	}
	handler := func(ctx context.Context, args *ServiceEventArgs) error {
		return handlerErr
	}
	eventManager.serviceEvents["prepublish"] = handler

	// Create invoke message
	invokeMsg := &InvokeServiceHandler{
		EventName:      "prepublish",
		Project:        createTestProjectConfigForEvents(),
		Service:        createTestServiceConfigForEvents(),
		ServiceContext: createTestServiceContextForEvents(),
	}

	// Invoke the handler
	resp, err := eventManager.onInvokeServiceHandler(ctx, invokeMsg)

	assert.NoError(t, err) // onInvokeServiceHandler doesn't return handler errors, it wraps them in the response

	// Verify the response message shows failure
	require.NotNil(t, resp)
	status := resp.GetServiceHandlerStatus()
	require.NotNil(t, status)
	assert.Equal(t, "prepublish", status.EventName)
	assert.Equal(t, "test-service", status.ServiceName)
	assert.Equal(t, "failed", status.Status)
	assert.Equal(t, "service handler failed", status.Message)

	// Verify the structured ExtensionError is also populated so the host can unwrap
	// it back into a typed LocalError (preserving the Suggestion and category).
	require.NotNil(t, status.Error, "expected structured ExtensionError on failed status")
	assert.Equal(t, "service handler failed", status.Error.GetMessage())
	assert.Equal(t, "Re-run with --debug", status.Error.GetSuggestion())
	require.NotNil(t, status.Error.GetLocalError())
	assert.Equal(t, "service_handler_failed", status.Error.GetLocalError().GetCode())
	assert.Equal(t, string(LocalErrorCategoryUser), status.Error.GetLocalError().GetCategory())
}

func TestEventManager_onInvokeServiceHandler_ServiceError(t *testing.T) {
	ctx := t.Context()
	client := &AzdClient{}

	eventManager := NewEventManager("microsoft.azd.demo", client, nil)

	handlerErr := &ServiceError{
		Message:     "service handler failed",
		ErrorCode:   "Conflict",
		StatusCode:  409,
		ServiceName: "management.azure.com",
		Suggestion:  "Wait for the active operation to finish",
		Links: []errorhandler.ErrorLink{{
			URL:   "https://aka.ms/azd-errors#conflict",
			Title: "Conflict troubleshooting",
		}},
	}
	handler := func(ctx context.Context, args *ServiceEventArgs) error {
		return handlerErr
	}
	eventManager.serviceEvents["prepublish"] = handler

	invokeMsg := &InvokeServiceHandler{
		EventName:      "prepublish",
		Project:        createTestProjectConfigForEvents(),
		Service:        createTestServiceConfigForEvents(),
		ServiceContext: createTestServiceContextForEvents(),
	}

	resp, err := eventManager.onInvokeServiceHandler(ctx, invokeMsg)

	assert.NoError(t, err)
	require.NotNil(t, resp)
	status := resp.GetServiceHandlerStatus()
	require.NotNil(t, status)
	require.NotNil(t, status.Error)
	require.NotNil(t, status.Error.GetServiceError())
	assert.Equal(t, "Conflict", status.Error.GetServiceError().GetErrorCode())
	assert.Equal(t, int32(409), status.Error.GetServiceError().GetStatusCode())
	assert.Equal(t, "management.azure.com", status.Error.GetServiceError().GetServiceName())
	assert.Equal(t, "Wait for the active operation to finish", status.Error.GetSuggestion())
	require.Len(t, status.Error.GetLinks(), 1)
	assert.Equal(t, "Conflict troubleshooting", status.Error.GetLinks()[0].GetTitle())
}

// Test onInvokeServiceHandler with no registered handler
func TestEventManager_onInvokeServiceHandler_NoHandler(t *testing.T) {
	ctx := t.Context()
	client := &AzdClient{}
	eventManager := NewEventManager("microsoft.azd.demo", client, nil)

	// Create invoke message for unregistered event
	invokeMsg := &InvokeServiceHandler{
		EventName:      "nonexistentevent",
		Project:        createTestProjectConfigForEvents(),
		Service:        createTestServiceConfigForEvents(),
		ServiceContext: createTestServiceContextForEvents(),
	}

	// Invoke should return an empty response (not an error, just no status message)
	resp, err := eventManager.onInvokeServiceHandler(ctx, invokeMsg)

	assert.NoError(t, err)
	assert.NotNil(t, resp)          // Returns empty EventMessage, not nil
	assert.Nil(t, resp.MessageType) // But the MessageType is nil (empty message)
}

// Test RemoveProjectEventHandler
func TestEventManager_RemoveProjectEventHandler(t *testing.T) {
	client := &AzdClient{}
	eventManager := NewEventManager("microsoft.azd.demo", client, nil)

	// Add a handler
	handler := func(ctx context.Context, args *ProjectEventArgs) error {
		return nil
	}
	eventManager.projectEvents["preprovision"] = handler

	// Verify it's there
	assert.Contains(t, eventManager.projectEvents, "preprovision")

	// Remove it
	eventManager.RemoveProjectEventHandler("preprovision")

	// Verify it's gone
	assert.NotContains(t, eventManager.projectEvents, "preprovision")
}

// Test RemoveServiceEventHandler
func TestEventManager_RemoveServiceEventHandler(t *testing.T) {
	client := &AzdClient{}
	eventManager := NewEventManager("microsoft.azd.demo", client, nil)

	// Add a handler
	handler := func(ctx context.Context, args *ServiceEventArgs) error {
		return nil
	}
	eventManager.serviceEvents["postpackage"] = handler

	// Verify it's there
	assert.Contains(t, eventManager.serviceEvents, "postpackage")

	// Remove it
	eventManager.RemoveServiceEventHandler("postpackage")

	// Verify it's gone
	assert.NotContains(t, eventManager.serviceEvents, "postpackage")
}

// Test Close
func TestEventManager_Close(t *testing.T) {
	client := &AzdClient{}
	eventManager := NewEventManager("microsoft.azd.demo", client, nil)

	// Close should always succeed (it's a no-op with the broker pattern)
	err := eventManager.Close()

	assert.NoError(t, err)
}
