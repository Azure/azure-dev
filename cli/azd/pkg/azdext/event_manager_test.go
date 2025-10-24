// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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

func (m *MockBidiStreamingClient[Req, Resp]) SendMsg(msg interface{}) error {
	args := m.Called(msg)
	return args.Error(0)
}

func (m *MockBidiStreamingClient[Req, Resp]) RecvMsg(msg interface{}) error {
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

	eventManager := NewEventManager(client)

	assert.NotNil(t, eventManager)
	assert.Equal(t, client, eventManager.azdClient)
	assert.NotNil(t, eventManager.projectEvents)
	assert.NotNil(t, eventManager.serviceEvents)
	assert.Empty(t, eventManager.projectEvents)
	assert.Empty(t, eventManager.serviceEvents)
}

// Test invokeProjectHandler with successful handler
func TestEventManager_invokeProjectHandler_Success(t *testing.T) {
	ctx := context.Background()
	client := &AzdClient{}
	mockStream := &MockBidiStreamingClient[*EventMessage, *EventMessage]{}

	// Setup mock expectation for status message
	mockStream.On("Send", mock.MatchedBy(func(msg *EventMessage) bool {
		// Verify the project handler status message
		if status := msg.GetProjectHandlerStatus(); status != nil {
			return status.EventName == "prerestore" &&
				status.Status == "completed" &&
				status.Message == ""
		}
		return false
	})).Return(nil)

	eventManager := NewEventManager(client)
	eventManager.stream = mockStream

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
	err := eventManager.invokeProjectHandler(ctx, invokeMsg)

	assert.NoError(t, err)
	assert.True(t, handlerCalled)
	assert.NotNil(t, receivedArgs)
	assert.Equal(t, "test-project", receivedArgs.Project.Name)

	mockStream.AssertExpectations(t)
}

// Test invokeProjectHandler with handler error
func TestEventManager_invokeProjectHandler_HandlerError(t *testing.T) {
	ctx := context.Background()
	client := &AzdClient{}
	mockStream := &MockBidiStreamingClient[*EventMessage, *EventMessage]{}

	// Setup mock expectation for error status message
	mockStream.On("Send", mock.MatchedBy(func(msg *EventMessage) bool {
		// Verify the project handler status message shows failure
		if status := msg.GetProjectHandlerStatus(); status != nil {
			return status.EventName == "postbuild" &&
				status.Status == "failed" &&
				status.Message == "handler failed"
		}
		return false
	})).Return(nil)

	eventManager := NewEventManager(client)
	eventManager.stream = mockStream

	// Add a test handler that fails
	handler := func(ctx context.Context, args *ProjectEventArgs) error {
		return errors.New("handler failed")
	}
	eventManager.projectEvents["postbuild"] = handler

	// Create invoke message
	invokeMsg := &InvokeProjectHandler{
		EventName: "postbuild",
		Project:   createTestProjectConfigForEvents(),
	}

	// Invoke the handler
	err := eventManager.invokeProjectHandler(ctx, invokeMsg)

	assert.NoError(t, err) // invokeProjectHandler doesn't return handler errors

	mockStream.AssertExpectations(t)
}

// Test invokeProjectHandler with no registered handler
func TestEventManager_invokeProjectHandler_NoHandler(t *testing.T) {
	ctx := context.Background()
	client := &AzdClient{}
	eventManager := NewEventManager(client)

	// Create invoke message for unregistered event
	invokeMsg := &InvokeProjectHandler{
		EventName: "nonexistentevent",
		Project:   createTestProjectConfigForEvents(),
	}

	// Invoke should be a no-op
	err := eventManager.invokeProjectHandler(ctx, invokeMsg)

	assert.NoError(t, err)
}

// Test invokeServiceHandler with successful handler
func TestEventManager_invokeServiceHandler_Success(t *testing.T) {
	ctx := context.Background()
	client := &AzdClient{}
	mockStream := &MockBidiStreamingClient[*EventMessage, *EventMessage]{}

	// Setup mock expectation for status message
	mockStream.On("Send", mock.MatchedBy(func(msg *EventMessage) bool {
		// Verify the service handler status message
		if status := msg.GetServiceHandlerStatus(); status != nil {
			return status.EventName == "prepackage" &&
				status.ServiceName == "test-service" &&
				status.Status == "completed" &&
				status.Message == ""
		}
		return false
	})).Return(nil)

	eventManager := NewEventManager(client)
	eventManager.stream = mockStream

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
	err := eventManager.invokeServiceHandler(ctx, invokeMsg)

	assert.NoError(t, err)
	assert.True(t, handlerCalled)
	assert.NotNil(t, receivedArgs)
	assert.Equal(t, "test-project", receivedArgs.Project.Name)
	assert.Equal(t, "test-service", receivedArgs.Service.Name)
	assert.NotNil(t, receivedArgs.ServiceContext)
	assert.NotNil(t, receivedArgs.ServiceContext.Package)
	assert.Len(t, receivedArgs.ServiceContext.Package, 1)
	assert.Equal(t, "test-package", receivedArgs.ServiceContext.Package[0].Metadata["name"])

	mockStream.AssertExpectations(t)
}

// Test invokeServiceHandler with nil ServiceContext (should default to empty)
func TestEventManager_invokeServiceHandler_NilServiceContext(t *testing.T) {
	ctx := context.Background()
	client := &AzdClient{}
	mockStream := &MockBidiStreamingClient[*EventMessage, *EventMessage]{}

	// Setup mock expectation for status message
	mockStream.On("Send", mock.MatchedBy(func(msg *EventMessage) bool {
		if status := msg.GetServiceHandlerStatus(); status != nil {
			return status.EventName == "postdeploy" &&
				status.ServiceName == "test-service" &&
				status.Status == "completed"
		}
		return false
	})).Return(nil)

	eventManager := NewEventManager(client)
	eventManager.stream = mockStream

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
	err := eventManager.invokeServiceHandler(ctx, invokeMsg)

	assert.NoError(t, err)
	assert.NotNil(t, receivedArgs)
	assert.NotNil(t, receivedArgs.ServiceContext) // Should be defaulted to empty instance

	mockStream.AssertExpectations(t)
}

// Test invokeServiceHandler with handler error
func TestEventManager_invokeServiceHandler_HandlerError(t *testing.T) {
	ctx := context.Background()
	client := &AzdClient{}
	mockStream := &MockBidiStreamingClient[*EventMessage, *EventMessage]{}

	// Setup mock expectation for error status message
	mockStream.On("Send", mock.MatchedBy(func(msg *EventMessage) bool {
		// Verify the service handler status message shows failure
		if status := msg.GetServiceHandlerStatus(); status != nil {
			return status.EventName == "prepublish" &&
				status.ServiceName == "test-service" &&
				status.Status == "failed" &&
				status.Message == "service handler failed"
		}
		return false
	})).Return(nil)

	eventManager := NewEventManager(client)
	eventManager.stream = mockStream

	// Add a test handler that fails
	handler := func(ctx context.Context, args *ServiceEventArgs) error {
		return errors.New("service handler failed")
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
	err := eventManager.invokeServiceHandler(ctx, invokeMsg)

	assert.NoError(t, err) // invokeServiceHandler doesn't return handler errors

	mockStream.AssertExpectations(t)
}

// Test invokeServiceHandler with no registered handler
func TestEventManager_invokeServiceHandler_NoHandler(t *testing.T) {
	ctx := context.Background()
	client := &AzdClient{}
	eventManager := NewEventManager(client)

	// Create invoke message for unregistered event
	invokeMsg := &InvokeServiceHandler{
		EventName:      "nonexistentevent",
		Project:        createTestProjectConfigForEvents(),
		Service:        createTestServiceConfigForEvents(),
		ServiceContext: createTestServiceContextForEvents(),
	}

	// Invoke should be a no-op
	err := eventManager.invokeServiceHandler(ctx, invokeMsg)

	assert.NoError(t, err)
}

// Test RemoveProjectEventHandler
func TestEventManager_RemoveProjectEventHandler(t *testing.T) {
	client := &AzdClient{}
	eventManager := NewEventManager(client)

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
	eventManager := NewEventManager(client)

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
	mockStream := &MockBidiStreamingClient[*EventMessage, *EventMessage]{}
	mockStream.On("CloseSend").Return(nil)

	client := &AzdClient{}
	eventManager := NewEventManager(client)
	eventManager.stream = mockStream

	err := eventManager.Close()

	assert.NoError(t, err)
	mockStream.AssertExpectations(t)
}

// Test Close with nil stream
func TestEventManager_Close_NilStream(t *testing.T) {
	client := &AzdClient{}
	eventManager := NewEventManager(client)

	err := eventManager.Close()

	assert.NoError(t, err)
}
