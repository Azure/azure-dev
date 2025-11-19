// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext/grpcbroker"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

// MockBidiStreamingServer mocks the gRPC bidirectional streaming server using generics
// Req represents the request message type, Resp represents the response message type
// In most cases they're the same (e.g., both *azdext.EventMessage), but the interface allows them to differ
//
// Usage examples:
//   - For EventService: MockBidiStreamingServer[*azdext.EventMessage, *azdext.EventMessage]
//   - For FrameworkService: MockBidiStreamingServer[*azdext.FrameworkServiceMessage, *azdext.FrameworkServiceMessage]
//   - For ServiceTargetService: MockBidiStreamingServer[*azdext.ServiceTargetMessage, *azdext.ServiceTargetMessage]
//
// The generic design allows this mock to be reused across all gRPC services
// that use bidirectional streaming in the azd codebase.
type MockBidiStreamingServer[Req any, Resp any] struct {
	mock.Mock
	sentMessages     []Resp
	receivedMessages []Req
	ctx              context.Context
}

func (m *MockBidiStreamingServer[Req, Resp]) Send(msg Resp) error {
	args := m.Called(msg)
	m.sentMessages = append(m.sentMessages, msg)
	return args.Error(0)
}

func (m *MockBidiStreamingServer[Req, Resp]) Recv() (Req, error) {
	args := m.Called()
	m.receivedMessages = append(m.receivedMessages, args.Get(0).(Req))
	return args.Get(0).(Req), args.Error(1)
}

func (m *MockBidiStreamingServer[Req, Resp]) Context() context.Context {
	if m.ctx != nil {
		return m.ctx
	}
	return context.Background()
}

func (m *MockBidiStreamingServer[Req, Resp]) SendMsg(msg interface{}) error {
	args := m.Called(msg)
	return args.Error(0)
}

func (m *MockBidiStreamingServer[Req, Resp]) RecvMsg(msg interface{}) error {
	args := m.Called(msg)
	return args.Error(0)
}

func (m *MockBidiStreamingServer[Req, Resp]) SetHeader(md metadata.MD) error {
	args := m.Called(md)
	return args.Error(0)
}

func (m *MockBidiStreamingServer[Req, Resp]) SendHeader(md metadata.MD) error {
	args := m.Called(md)
	return args.Error(0)
}

func (m *MockBidiStreamingServer[Req, Resp]) SetTrailer(md metadata.MD) {
	m.Called(md)
}

// Type aliases for convenience when working with specific message types
type MockEventStreamingServer = MockBidiStreamingServer[*azdext.EventMessage, *azdext.EventMessage]

// Test helpers
func createTestEventService() (*eventService, *MockEventStreamingServer) {
	mockStream := &MockEventStreamingServer{}
	extensionManager := &extensions.Manager{}

	// Create lazy environment manager (mock)
	lazyEnvManager := lazy.NewLazy(func() (environment.Manager, error) {
		return nil, nil // Tests don't need actual environment manager
	})

	// Create lazy project with simple test config
	lazyProject := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		projectConfig := &project.ProjectConfig{
			Name: "test-project",
			Services: map[string]*project.ServiceConfig{
				"api": {
					Name:         "api",
					Language:     project.ServiceLanguageTypeScript,
					Host:         project.ContainerAppTarget,
					RelativePath: "./api",
				},
				"web": {
					Name:         "web",
					Language:     project.ServiceLanguageTypeScript,
					Host:         project.StaticWebAppTarget,
					RelativePath: "./web",
				},
			},
		}
		// Initialize the event dispatcher
		projectConfig.EventDispatcher = ext.NewEventDispatcher[project.ProjectLifecycleEventArgs]()

		// Initialize service event dispatchers
		for _, serviceConfig := range projectConfig.Services {
			serviceConfig.EventDispatcher = ext.NewEventDispatcher[project.ServiceLifecycleEventArgs]()
		}

		return projectConfig, nil
	})

	// Create lazy environment
	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		env := environment.NewWithValues("test-env", map[string]string{
			"AZURE_SUBSCRIPTION_ID": "test-sub-id",
			"AZURE_LOCATION":        "eastus2",
		})
		return env, nil
	})

	console := mockinput.NewMockConsole()

	service := NewEventService(extensionManager, lazyEnvManager, lazyProject, lazyEnv, console)
	return service.(*eventService), mockStream
}

func createTestExtension() *extensions.Extension {
	return &extensions.Extension{
		Id:          "test.extension",
		DisplayName: "Test Extension",
		Version:     "1.0.0",
		// Don't call Initialize() as it causes hangs in tests
	}
}

func TestEventService_handleSubscribeProjectEvent(t *testing.T) {
	service, _ := createTestEventService()
	extension := createTestExtension()
	ctx := context.Background()

	tests := []struct {
		name         string
		subscribeMsg *azdext.SubscribeProjectEvent
		expectError  bool
	}{
		{
			name: "subscribe to single event",
			subscribeMsg: &azdext.SubscribeProjectEvent{
				EventNames: []string{"prepackage"},
			},
			expectError: false,
		},
		{
			name: "subscribe to multiple events",
			subscribeMsg: &azdext.SubscribeProjectEvent{
				EventNames: []string{"prepackage", "postpackage", "predeploy"},
			},
			expectError: false,
		},
		{
			name: "subscribe to empty events",
			subscribeMsg: &azdext.SubscribeProjectEvent{
				EventNames: []string{},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock broker (nil is acceptable for these tests since handlers aren't executed)
			var mockBroker *grpcbroker.MessageBroker[azdext.EventMessage]

			err := service.onSubscribeProjectEvent(ctx, extension, tt.subscribeMsg, mockBroker)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)

			// Note: Handler registration is internal to project.ProjectConfig.
			// Full integration testing would require firing events and verifying behavior.
		})
	}
}

func TestEventService_handleSubscribeServiceEvent(t *testing.T) {
	service, _ := createTestEventService()
	extension := createTestExtension()
	ctx := context.Background()

	tests := []struct {
		name         string
		subscribeMsg *azdext.SubscribeServiceEvent
		expectError  bool
	}{
		{
			name: "subscribe to service event for all services",
			subscribeMsg: &azdext.SubscribeServiceEvent{
				EventNames: []string{"prepackage"},
				Language:   "",
				Host:       "",
			},
			expectError: false,
		},
		{
			name: "subscribe to service event filtered by language",
			subscribeMsg: &azdext.SubscribeServiceEvent{
				EventNames: []string{"prepackage"},
				Language:   "ts",
				Host:       "",
			},
			expectError: false,
		},
		{
			name: "subscribe to service event filtered by host",
			subscribeMsg: &azdext.SubscribeServiceEvent{
				EventNames: []string{"prepackage"},
				Language:   "",
				Host:       "containerapp",
			},
			expectError: false,
		},
		{
			name: "subscribe to service event with multiple filters",
			subscribeMsg: &azdext.SubscribeServiceEvent{
				EventNames: []string{"prepackage"},
				Language:   "ts",
				Host:       "staticwebapp",
			},
			expectError: false,
		},
		{
			name: "subscribe to multiple service events",
			subscribeMsg: &azdext.SubscribeServiceEvent{
				EventNames: []string{"prepackage", "postpackage"},
				Language:   "",
				Host:       "",
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock broker (nil is acceptable for these tests since handlers aren't executed)
			var mockBroker *grpcbroker.MessageBroker[azdext.EventMessage]

			err := service.onSubscribeServiceEvent(ctx, extension, tt.subscribeMsg, mockBroker)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)

			// Note: Handler registration is internal to project.ProjectConfig.
			// Full integration testing would require firing events and verifying behavior.
		})
	}
}

func TestEventService_createProjectEventHandler(t *testing.T) {
	service, _ := createTestEventService()
	extension := createTestExtension()
	eventName := "prepackage"

	// Create a context with metadata containing extension claims (simulating the stream context)
	md := metadata.New(map[string]string{
		"authorization": "fake-token", // Extension claims would normally be in this token
	})
	streamCtx := metadata.NewIncomingContext(context.Background(), md)

	// Create a mock broker (nil is acceptable since we're not executing the handler)
	var mockBroker *grpcbroker.MessageBroker[azdext.EventMessage]

	// Create the handler
	handler := service.createProjectEventHandler(streamCtx, extension, eventName, mockBroker)
	require.NotNil(t, handler)

	// Test that the handler function is created correctly
	// We won't execute it since that would require complex async setup with broker
	assert.NotNil(t, handler)
}

func TestEventService_createServiceEventHandler(t *testing.T) {
	service, _ := createTestEventService()
	extension := createTestExtension()
	eventName := "prepackage"

	serviceConfig := &project.ServiceConfig{
		Name:         "test-service",
		Language:     project.ServiceLanguageTypeScript,
		Host:         project.ContainerAppTarget,
		RelativePath: "./test-service",
	}

	// Create a context with metadata containing extension claims (simulating the stream context)
	md := metadata.New(map[string]string{
		"authorization": "fake-token", // Extension claims would normally be in this token
	})
	streamCtx := metadata.NewIncomingContext(context.Background(), md)

	// Create a mock broker (nil is acceptable since we're not executing the handler)
	var mockBroker *grpcbroker.MessageBroker[azdext.EventMessage]

	// Create the handler
	handler := service.createServiceEventHandler(streamCtx, serviceConfig, extension, eventName, mockBroker)
	require.NotNil(t, handler)

	// Test that the handler function is created correctly
	assert.NotNil(t, handler)
}

func TestEventService_New(t *testing.T) {
	extensionManager := &extensions.Manager{}

	lazyEnvManager := lazy.NewLazy(func() (environment.Manager, error) {
		return nil, nil // Tests don't need actual environment manager
	})

	lazyProject := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return &project.ProjectConfig{Name: "test"}, nil
	})

	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		return environment.NewWithValues("test", map[string]string{}), nil
	})

	console := mockinput.NewMockConsole()

	service := NewEventService(extensionManager, lazyEnvManager, lazyProject, lazyEnv, console)

	assert.NotNil(t, service)

	// Verify type assertion works
	eventSvc, ok := service.(*eventService)
	assert.True(t, ok)
	assert.NotNil(t, eventSvc.extensionManager)
	assert.NotNil(t, eventSvc.lazyProject)
	assert.NotNil(t, eventSvc.lazyEnv)
	assert.NotNil(t, eventSvc.console)
}
