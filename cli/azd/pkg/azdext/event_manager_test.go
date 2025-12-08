// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"testing"

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

	eventManager := NewEventManager("microsoft.azd.demo", client)

	assert.NotNil(t, eventManager)
	assert.Equal(t, client, eventManager.client)
	assert.NotNil(t, eventManager.projectEvents)
	assert.NotNil(t, eventManager.serviceEvents)
	assert.Empty(t, eventManager.projectEvents)
	assert.Empty(t, eventManager.serviceEvents)
}

// Test onInvokeProjectHandler with successful handler
func TestEventManager_onInvokeProjectHandler_Success(t *testing.T) {
	ctx := context.Background()
	client := &AzdClient{}

	eventManager := NewEventManager("microsoft.azd.demo", client)

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

// Test onInvokeProjectHandler with handler error
func TestEventManager_onInvokeProjectHandler_HandlerError(t *testing.T) {
	ctx := context.Background()
	client := &AzdClient{}

	eventManager := NewEventManager("microsoft.azd.demo", client)

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
	resp, err := eventManager.onInvokeProjectHandler(ctx, invokeMsg)

	assert.NoError(t, err) // onInvokeProjectHandler doesn't return handler errors, it wraps them in the response

	// Verify the response message shows failure
	require.NotNil(t, resp)
	status := resp.GetProjectHandlerStatus()
	require.NotNil(t, status)
	assert.Equal(t, "postbuild", status.EventName)
	assert.Equal(t, "failed", status.Status)
	assert.Equal(t, "handler failed", status.Message)
}

// Test onInvokeProjectHandler with no registered handler
func TestEventManager_onInvokeProjectHandler_NoHandler(t *testing.T) {
	ctx := context.Background()
	client := &AzdClient{}
	eventManager := NewEventManager("microsoft.azd.demo", client)

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
	ctx := context.Background()
	client := &AzdClient{}

	eventManager := NewEventManager("microsoft.azd.demo", client)

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
	ctx := context.Background()
	client := &AzdClient{}

	eventManager := NewEventManager("microsoft.azd.demo", client)

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
	ctx := context.Background()
	client := &AzdClient{}

	eventManager := NewEventManager("microsoft.azd.demo", client)

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
}

// Test onInvokeServiceHandler with no registered handler
func TestEventManager_onInvokeServiceHandler_NoHandler(t *testing.T) {
	ctx := context.Background()
	client := &AzdClient{}
	eventManager := NewEventManager("microsoft.azd.demo", client)

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
	eventManager := NewEventManager("microsoft.azd.demo", client)

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
	eventManager := NewEventManager("microsoft.azd.demo", client)

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
	eventManager := NewEventManager("microsoft.azd.demo", client)

	// Close should always succeed (it's a no-op with the broker pattern)
	err := eventManager.Close()

	assert.NoError(t, err)
}
