// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"sync"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
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

	service := NewEventService(extensionManager, lazyProject, lazyEnv, console)
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
	service, mockStream := createTestEventService()
	extension := createTestExtension()
	ctx := context.Background()

	tests := []struct {
		name           string
		subscribeMsg   *azdext.SubscribeProjectEvent
		expectError    bool
		expectedEvents int
	}{
		{
			name: "subscribe to single event",
			subscribeMsg: &azdext.SubscribeProjectEvent{
				EventNames: []string{"prepackage"},
			},
			expectError:    false,
			expectedEvents: 1,
		},
		{
			name: "subscribe to multiple events",
			subscribeMsg: &azdext.SubscribeProjectEvent{
				EventNames: []string{"prepackage", "postpackage", "predeploy"},
			},
			expectError:    false,
			expectedEvents: 3,
		},
		{
			name: "subscribe to empty events",
			subscribeMsg: &azdext.SubscribeProjectEvent{
				EventNames: []string{},
			},
			expectError:    false,
			expectedEvents: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear previous events
			service.projectEvents = sync.Map{}

			err := service.handleSubscribeProjectEvent(ctx, extension, tt.subscribeMsg, mockStream)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)

			// Verify correct number of events were stored
			eventCount := 0
			service.projectEvents.Range(func(key, value interface{}) bool {
				eventCount++

				// Verify the key format: extension.eventName
				keyStr := key.(string)
				assert.Contains(t, keyStr, extension.Id)

				// Verify channel was created
				ch := value.(chan *azdext.ProjectHandlerStatus)
				assert.NotNil(t, ch)
				assert.Equal(t, 1, cap(ch))

				return true
			})
			assert.Equal(t, tt.expectedEvents, eventCount)
		})
	}
}

func TestEventService_handleSubscribeServiceEvent(t *testing.T) {
	service, mockStream := createTestEventService()
	extension := createTestExtension()
	ctx := context.Background()

	tests := []struct {
		name           string
		subscribeMsg   *azdext.SubscribeServiceEvent
		expectError    bool
		expectedEvents int
	}{
		{
			name: "subscribe to service event for all services",
			subscribeMsg: &azdext.SubscribeServiceEvent{
				EventNames: []string{"prepackage"},
				Language:   "", // empty means all languages
				Host:       "", // empty means all hosts
			},
			expectError:    false,
			expectedEvents: 2, // Two services in test config
		},
		{
			name: "subscribe to service event filtered by language",
			subscribeMsg: &azdext.SubscribeServiceEvent{
				EventNames: []string{"prepackage"},
				Language:   "ts", // TypeScript constant is "ts", not "typescript"
				Host:       "",
			},
			expectError:    false,
			expectedEvents: 2, // Both services are TypeScript
		},
		{
			name: "subscribe to service event filtered by host",
			subscribeMsg: &azdext.SubscribeServiceEvent{
				EventNames: []string{"prepackage"},
				Language:   "",
				Host:       "containerapp",
			},
			expectError:    false,
			expectedEvents: 1, // Only api service is containerapp
		},
		{
			name: "subscribe to service event with multiple filters",
			subscribeMsg: &azdext.SubscribeServiceEvent{
				EventNames: []string{"prepackage"},
				Language:   "ts", // TypeScript constant is "ts", not "typescript"
				Host:       "staticwebapp",
			},
			expectError:    false,
			expectedEvents: 1, // Only web service matches both filters
		},
		{
			name: "subscribe to multiple service events",
			subscribeMsg: &azdext.SubscribeServiceEvent{
				EventNames: []string{"prepackage", "postpackage"},
				Language:   "",
				Host:       "",
			},
			expectError:    false,
			expectedEvents: 4, // 2 events * 2 services
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear previous events
			service.serviceEvents = sync.Map{}

			err := service.handleSubscribeServiceEvent(ctx, extension, tt.subscribeMsg, mockStream)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)

			// Verify correct number of events were stored
			eventCount := 0
			service.serviceEvents.Range(func(key, value interface{}) bool {
				eventCount++

				// Verify the key format: extension.serviceName.eventName
				keyStr := key.(string)
				assert.Contains(t, keyStr, extension.Id)

				// Verify channel was created
				ch := value.(chan *azdext.ServiceHandlerStatus)
				assert.NotNil(t, ch)
				assert.Equal(t, 1, cap(ch))

				return true
			})
			assert.Equal(t, tt.expectedEvents, eventCount)
		})
	}
}

func TestEventService_createProjectEventHandler(t *testing.T) {
	service, mockStream := createTestEventService()
	extension := createTestExtension()
	eventName := "prepackage"

	// Create the handler
	handler := service.createProjectEventHandler(mockStream, extension, eventName)
	require.NotNil(t, handler)

	// Test that the handler function is created correctly
	// We won't execute it since that would require complex async setup
	assert.NotNil(t, handler)
}

func TestEventService_createServiceEventHandler(t *testing.T) {
	service, mockStream := createTestEventService()
	extension := createTestExtension()
	eventName := "prepackage"

	serviceConfig := &project.ServiceConfig{
		Name:         "test-service",
		Language:     project.ServiceLanguageTypeScript,
		Host:         project.ContainerAppTarget,
		RelativePath: "./test-service",
	}

	// Create the handler
	handler := service.createServiceEventHandler(mockStream, serviceConfig, extension, eventName)
	require.NotNil(t, handler)

	// Test that the handler function is created correctly
	// We won't execute it since that would require complex async setup
	assert.NotNil(t, handler)
}

func TestEventService_sendProjectInvokeMessage(t *testing.T) {
	service, mockStream := createTestEventService()
	eventName := "prepackage"

	// Setup mock expectations - capture the sent message
	var sentMessage *azdext.EventMessage
	mockStream.On("Send", mock.AnythingOfType("*azdext.EventMessage")).Run(func(args mock.Arguments) {
		sentMessage = args.Get(0).(*azdext.EventMessage)
	}).Return(nil)

	// Create test project lifecycle event args
	args := project.ProjectLifecycleEventArgs{
		Project: &project.ProjectConfig{
			Name: "test-project",
		},
		Args: map[string]any{},
	}

	// Execute the method
	err := service.sendProjectInvokeMessage(mockStream, eventName, args)

	// Verify no error occurred
	assert.NoError(t, err)

	// Verify the message was sent
	mockStream.AssertCalled(t, "Send", mock.AnythingOfType("*azdext.EventMessage"))

	// Verify the message structure
	require.NotNil(t, sentMessage)
	require.NotNil(t, sentMessage.GetInvokeProjectHandler())

	invokeMsg := sentMessage.GetInvokeProjectHandler()
	assert.Equal(t, eventName, invokeMsg.EventName)
	assert.NotNil(t, invokeMsg.Project)
	assert.Equal(t, "test-project", invokeMsg.Project.Name)
}

func TestEventService_sendServiceInvokeMessage(t *testing.T) {
	service, mockStream := createTestEventService()
	eventName := "prepackage"

	// Setup mock expectations - capture the sent message
	var sentMessage *azdext.EventMessage
	mockStream.On("Send", mock.AnythingOfType("*azdext.EventMessage")).Run(func(args mock.Arguments) {
		sentMessage = args.Get(0).(*azdext.EventMessage)
	}).Return(nil)

	serviceConfig := &project.ServiceConfig{
		Name:         "test-service",
		Language:     project.ServiceLanguageTypeScript,
		Host:         project.ContainerAppTarget,
		RelativePath: "./test-service",
	}

	// Create test service lifecycle event args with ServiceContext
	args := project.ServiceLifecycleEventArgs{
		Project: &project.ProjectConfig{
			Name: "test-project",
		},
		Service:        serviceConfig,
		ServiceContext: project.NewServiceContext(),
		Args:           map[string]any{},
	}

	// Execute the method
	err := service.sendServiceInvokeMessage(mockStream, eventName, args)

	// Verify no error occurred
	assert.NoError(t, err)

	// Verify the message was sent
	mockStream.AssertCalled(t, "Send", mock.AnythingOfType("*azdext.EventMessage"))

	// Verify the message structure
	require.NotNil(t, sentMessage)
	require.NotNil(t, sentMessage.GetInvokeServiceHandler())

	invokeMsg := sentMessage.GetInvokeServiceHandler()
	assert.Equal(t, eventName, invokeMsg.EventName)
	assert.NotNil(t, invokeMsg.Project)
	assert.Equal(t, "test-project", invokeMsg.Project.Name)
	assert.NotNil(t, invokeMsg.Service)
	assert.Equal(t, "test-service", invokeMsg.Service.Name)

	// Verify ServiceContext was included
	assert.NotNil(t, invokeMsg.ServiceContext)
}

func TestEventService_New(t *testing.T) {
	extensionManager := &extensions.Manager{}

	lazyProject := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return &project.ProjectConfig{Name: "test"}, nil
	})

	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		return environment.NewWithValues("test", map[string]string{}), nil
	})

	console := mockinput.NewMockConsole()

	service := NewEventService(extensionManager, lazyProject, lazyEnv, console)

	assert.NotNil(t, service)

	// Verify type assertion works
	eventSvc, ok := service.(*eventService)
	assert.True(t, ok)
	assert.NotNil(t, eventSvc.extensionManager)
	assert.NotNil(t, eventSvc.lazyProject)
	assert.NotNil(t, eventSvc.lazyEnv)
	assert.NotNil(t, eventSvc.console)
}
