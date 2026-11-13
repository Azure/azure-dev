// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
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

func TestEventManager_onInvokeProjectHandler_CanceledHandlerReturnsNil(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	eventManager := NewEventManager("microsoft.azd.demo", &AzdClient{}, nil)
	eventManager.projectEvents["prepackage"] = func(context.Context, *ProjectEventArgs) error {
		cancel()
		return nil
	}

	resp, err := eventManager.onInvokeProjectHandler(ctx, &InvokeProjectHandler{
		EventName: "prepackage",
		Project:   createTestProjectConfigForEvents(),
	})

	require.NoError(t, err)
	status := resp.GetProjectHandlerStatus()
	require.NotNil(t, status)
	require.Equal(t, "failed", status.Status)
	require.Equal(t, context.Canceled.Error(), status.Message)
	require.ErrorIs(t, UnwrapError(status.Error), context.Canceled)
}

func TestEventManager_onInvokeProjectHandler_DeadlineHandlerReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	eventManager := NewEventManager("microsoft.azd.demo", &AzdClient{}, nil)
	eventManager.projectEvents["prepackage"] = func(ctx context.Context, _ *ProjectEventArgs) error {
		cancel(context.DeadlineExceeded)
		return ctx.Err()
	}

	resp, err := eventManager.onInvokeProjectHandler(ctx, &InvokeProjectHandler{
		EventName: "prepackage",
		Project:   createTestProjectConfigForEvents(),
	})

	require.NoError(t, err)
	status := resp.GetProjectHandlerStatus()
	require.NotNil(t, status)
	require.Equal(t, "failed", status.Status)
	require.Equal(t, context.DeadlineExceeded.Error(), status.Message)
	require.ErrorIs(t, UnwrapError(status.Error), context.DeadlineExceeded)
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

func TestEventManager_onInvokeServiceHandler_CanceledHandlerReturnsNil(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	eventManager := NewEventManager("microsoft.azd.demo", &AzdClient{}, nil)
	eventManager.serviceEvents["prepackage"] = func(context.Context, *ServiceEventArgs) error {
		cancel()
		return nil
	}

	resp, err := eventManager.onInvokeServiceHandler(ctx, &InvokeServiceHandler{
		EventName:      "prepackage",
		Project:        createTestProjectConfigForEvents(),
		Service:        createTestServiceConfigForEvents(),
		ServiceContext: createTestServiceContextForEvents(),
	})

	require.NoError(t, err)
	status := resp.GetServiceHandlerStatus()
	require.NotNil(t, status)
	require.Equal(t, "failed", status.Status)
	require.Equal(t, context.Canceled.Error(), status.Message)
	require.ErrorIs(t, UnwrapError(status.Error), context.Canceled)
}

func TestEventManager_onInvokeServiceHandler_DeadlineHandlerReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	eventManager := NewEventManager("microsoft.azd.demo", &AzdClient{}, nil)
	eventManager.serviceEvents["prepackage"] = func(ctx context.Context, _ *ServiceEventArgs) error {
		cancel(context.DeadlineExceeded)
		return ctx.Err()
	}

	resp, err := eventManager.onInvokeServiceHandler(ctx, &InvokeServiceHandler{
		EventName:      "prepackage",
		Project:        createTestProjectConfigForEvents(),
		Service:        createTestServiceConfigForEvents(),
		ServiceContext: createTestServiceContextForEvents(),
	})

	require.NoError(t, err)
	status := resp.GetServiceHandlerStatus()
	require.NotNil(t, status)
	require.Equal(t, "failed", status.Status)
	require.Equal(t, context.DeadlineExceeded.Error(), status.Message)
	require.ErrorIs(t, UnwrapError(status.Error), context.DeadlineExceeded)
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

func TestEventManager_onInvokeServiceHandler_ResponseHandlerMessages(t *testing.T) {
	info := &ServiceEventMessage{
		Kind:    ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_INFO,
		Message: "Deployment guidance.",
	}
	warning := &ServiceEventMessage{
		Kind:       ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_WARNING,
		Message:    "Assign the required role.",
		Suggestion: "Ask an owner to grant access.",
		Links:      []*ErrorLink{{Title: "Role guide", Url: "https://aka.ms/role"}},
	}

	tests := []struct {
		name         string
		response     *ServiceEventResponse
		handlerErr   error
		wantStatus   string
		wantErr      []string
		wantMessages []*ServiceEventMessage
	}{
		{
			name:         "valid messages are returned",
			response:     &ServiceEventResponse{Messages: []*ServiceEventMessage{info, warning}},
			wantStatus:   "completed",
			wantMessages: []*ServiceEventMessage{info, warning},
		},
		{
			name:       "nil response completes without messages",
			wantStatus: "completed",
		},
		{
			name:         "messages are kept when the handler fails",
			response:     &ServiceEventResponse{Messages: []*ServiceEventMessage{warning}},
			handlerErr:   errors.New("role check failed"),
			wantStatus:   "failed",
			wantErr:      []string{"role check failed"},
			wantMessages: []*ServiceEventMessage{warning},
		},
		{
			name: "invalid messages fail the handler and valid messages are kept",
			response: &ServiceEventResponse{Messages: []*ServiceEventMessage{
				nil,
				{Message: "No kind"},
				{Kind: ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_INFO, Message: " "},
				{
					Kind:    ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_WARNING,
					Message: "Missing link URL",
					Links:   []*ErrorLink{{Title: "No URL"}},
				},
				warning,
			}},
			wantStatus: "failed",
			wantErr: []string{
				"message 1: message is nil",
				"message 2: unsupported message kind",
				"message 3: message text is required",
				"message 4: link 1 URL is required",
			},
			wantMessages: []*ServiceEventMessage{warning},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eventManager := NewEventManager("microsoft.azd.demo", &AzdClient{}, nil)
			eventManager.serviceResponseEvents["predeploy"] = func(
				context.Context,
				*ServiceEventArgs,
			) (*ServiceEventResponse, error) {
				return tt.response, tt.handlerErr
			}

			resp, err := eventManager.onInvokeServiceHandler(t.Context(), &InvokeServiceHandler{
				EventName:      "predeploy",
				Project:        createTestProjectConfigForEvents(),
				Service:        createTestServiceConfigForEvents(),
				ServiceContext: createTestServiceContextForEvents(),
			})
			require.NoError(t, err)

			status := resp.GetServiceHandlerStatus()
			require.NotNil(t, status)
			require.Equal(t, "predeploy", status.GetEventName())
			require.Equal(t, "test-service", status.GetServiceName())
			require.Equal(t, tt.wantStatus, status.GetStatus())
			for _, wantErr := range tt.wantErr {
				require.Contains(t, status.GetMessage(), wantErr)
			}
			require.Equal(t, tt.wantMessages, status.GetMessages())
		})
	}
}

func TestEventManager_AddServiceEventResponseHandler_Validation(t *testing.T) {
	handler := func(context.Context, *ServiceEventArgs) (*ServiceEventResponse, error) {
		return nil, nil
	}

	tests := []struct {
		name      string
		eventName string
		handler   ServiceEventResponseHandler
		setup     func(*EventManager)
		wantErr   string
	}{
		{
			name:      "unsupported event",
			eventName: "prepackage",
			handler:   handler,
			wantErr:   "use predeploy or postdeploy",
		},
		{
			name:      "nil handler",
			eventName: "predeploy",
			wantErr:   "is nil",
		},
		{
			name:      "existing service handler",
			eventName: "predeploy",
			handler:   handler,
			setup: func(eventManager *EventManager) {
				eventManager.serviceEvents["predeploy"] = func(context.Context, *ServiceEventArgs) error {
					return nil
				}
			},
			wantErr: "already registered",
		},
		{
			name:      "existing response handler",
			eventName: "postdeploy",
			handler:   handler,
			setup: func(eventManager *EventManager) {
				eventManager.serviceResponseEvents["postdeploy"] = handler
			},
			wantErr: "already registered",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eventManager := NewEventManager("microsoft.azd.demo", &AzdClient{}, nil)
			if tt.setup != nil {
				tt.setup(eventManager)
			}

			err := eventManager.AddServiceEventResponseHandler(t.Context(), tt.eventName, tt.handler, nil)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
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
