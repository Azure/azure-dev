// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/state"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
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

func (m *MockBidiStreamingServer[Req, Resp]) SendMsg(msg any) error {
	args := m.Called(msg)
	return args.Error(0)
}

func (m *MockBidiStreamingServer[Req, Resp]) RecvMsg(msg any) error {
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

type noOpEnvironmentManager struct{}

func (m *noOpEnvironmentManager) Create(ctx context.Context, spec environment.Spec) (*environment.Environment, error) {
	return nil, nil
}

func (m *noOpEnvironmentManager) LoadOrInitInteractive(
	ctx context.Context,
	name string,
) (*environment.Environment, error) {
	return nil, nil
}

func (m *noOpEnvironmentManager) List(ctx context.Context) ([]*environment.Description, error) {
	return nil, nil
}

func (m *noOpEnvironmentManager) Get(ctx context.Context, name string) (*environment.Environment, error) {
	return nil, nil
}

func (m *noOpEnvironmentManager) Save(ctx context.Context, env *environment.Environment) error {
	return nil
}

func (m *noOpEnvironmentManager) SaveWithOptions(
	ctx context.Context,
	env *environment.Environment,
	options *environment.SaveOptions,
) error {
	return nil
}

func (m *noOpEnvironmentManager) Reload(ctx context.Context, env *environment.Environment) error {
	return nil
}

func (m *noOpEnvironmentManager) Delete(ctx context.Context, name string) error {
	return nil
}

func (m *noOpEnvironmentManager) EnvPath(env *environment.Environment) string {
	return ""
}

func (m *noOpEnvironmentManager) ConfigPath(env *environment.Environment) string {
	return ""
}

func (m *noOpEnvironmentManager) InvalidateEnvCache(ctx context.Context, envName string) error {
	return nil
}

func (m *noOpEnvironmentManager) GetStateCacheManager() *state.StateCacheManager {
	return nil
}

// Test helpers
func createTestEventService() (*eventService, *MockEventStreamingServer) {
	mockStream := &MockEventStreamingServer{}
	extensionManager := &extensions.Manager{}

	// Create lazy environment manager (mock)
	lazyEnvManager := lazy.NewLazy(func() (environment.Manager, error) {
		return &noOpEnvironmentManager{}, nil
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

type scriptedEventStream struct {
	ctx    context.Context
	recvCh chan *azdext.EventMessage
	sendFn func(*azdext.EventMessage) error
}

func (s *scriptedEventStream) Send(msg *azdext.EventMessage) error {
	return s.sendFn(msg)
}

func (s *scriptedEventStream) Recv() (*azdext.EventMessage, error) {
	msg, ok := <-s.recvCh
	if !ok {
		return nil, io.EOF
	}

	return msg, nil
}

func createBrokerForEventHandler(
	t *testing.T,
	extensionID string,
	responseFn func(*azdext.EventMessage) *azdext.EventMessage,
) (*grpcbroker.MessageBroker[azdext.EventMessage], context.Context, func()) {
	t.Helper()

	streamCtx := extensions.WithClaimsContext(context.Background(), &extensions.ExtensionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: extensionID,
		},
	})

	stream := &scriptedEventStream{
		ctx:    streamCtx,
		recvCh: make(chan *azdext.EventMessage, 1),
	}
	stream.sendFn = func(msg *azdext.EventMessage) error {
		if response := responseFn(msg); response != nil {
			stream.recvCh <- response
		}

		return nil
	}

	brokerCtx, cancel := context.WithCancel(streamCtx)
	broker := grpcbroker.NewMessageBroker(stream, azdext.NewEventMessageEnvelope(), extensionID, nil)

	go func() {
		_ = broker.Run(brokerCtx)
	}()

	require.NoError(t, broker.Ready(t.Context()))

	cleanup := func() {
		close(stream.recvCh)
		cancel()
	}

	return broker, streamCtx, cleanup
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
			expectError: true,
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

func TestEventService_createProjectEventHandler_RoundTripsStructuredError(t *testing.T) {
	service, _ := createTestEventService()
	extension := createTestExtension()
	projectConfig, err := service.lazyProject.GetValue()
	require.NoError(t, err)

	broker, streamCtx, cleanup := createBrokerForEventHandler(
		t,
		extension.Id,
		func(msg *azdext.EventMessage) *azdext.EventMessage {
			invoke := msg.GetInvokeProjectHandler()
			require.NotNil(t, invoke)

			return &azdext.EventMessage{
				MessageType: &azdext.EventMessage_ProjectHandlerStatus{
					ProjectHandlerStatus: &azdext.ProjectHandlerStatus{
						EventName: invoke.EventName,
						Status:    "failed",
						Message:   "extension project hook failed",
						Error: azdext.WrapError(&azdext.LocalError{
							Message:    "extension project hook failed",
							Code:       "hook_failed",
							Category:   azdext.LocalErrorCategoryValidation,
							Suggestion: "Fix the extension config and retry",
							Links: []errorhandler.ErrorLink{{
								URL:   "https://aka.ms/azd-errors#hook-failed",
								Title: "Hook troubleshooting",
							}},
						}),
					},
				},
			}
		},
	)
	defer cleanup()

	handler := service.createProjectEventHandler(streamCtx, extension, "prepackage", broker)
	err = handler(t.Context(), project.ProjectLifecycleEventArgs{Project: projectConfig})
	require.Error(t, err)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, "extension project hook failed", localErr.Message)
	assert.Equal(t, "hook_failed", localErr.Code)
	assert.Equal(t, azdext.LocalErrorCategoryValidation, localErr.Category)
	assert.Equal(t, "Fix the extension config and retry", localErr.Suggestion)
	require.Len(t, localErr.Links, 1)
	assert.Equal(t, "Hook troubleshooting", localErr.Links[0].Title)
}

func TestEventService_createProjectEventHandler_BackCompatFailedMessage(t *testing.T) {
	service, _ := createTestEventService()
	extension := createTestExtension()
	projectConfig, err := service.lazyProject.GetValue()
	require.NoError(t, err)

	broker, streamCtx, cleanup := createBrokerForEventHandler(
		t,
		extension.Id,
		func(msg *azdext.EventMessage) *azdext.EventMessage {
			invoke := msg.GetInvokeProjectHandler()
			require.NotNil(t, invoke)

			return &azdext.EventMessage{
				MessageType: &azdext.EventMessage_ProjectHandlerStatus{
					ProjectHandlerStatus: &azdext.ProjectHandlerStatus{
						EventName: invoke.EventName,
						Status:    "failed",
						Message:   "old host failure message",
					},
				},
			}
		},
	)
	defer cleanup()

	handler := service.createProjectEventHandler(streamCtx, extension, "prepackage", broker)
	err = handler(t.Context(), project.ProjectLifecycleEventArgs{Project: projectConfig})
	require.EqualError(t, err, "extension test.extension project hook prepackage failed: old host failure message")
}

func TestEventService_createServiceEventHandler_RoundTripsStructuredError(t *testing.T) {
	service, _ := createTestEventService()
	extension := createTestExtension()
	projectConfig, err := service.lazyProject.GetValue()
	require.NoError(t, err)
	serviceConfig := projectConfig.Services["api"]
	require.NotNil(t, serviceConfig)

	broker, streamCtx, cleanup := createBrokerForEventHandler(
		t,
		extension.Id,
		func(msg *azdext.EventMessage) *azdext.EventMessage {
			invoke := msg.GetInvokeServiceHandler()
			require.NotNil(t, invoke)

			return &azdext.EventMessage{
				MessageType: &azdext.EventMessage_ServiceHandlerStatus{
					ServiceHandlerStatus: &azdext.ServiceHandlerStatus{
						EventName:   invoke.EventName,
						ServiceName: invoke.Service.Name,
						Status:      "failed",
						Message:     "extension service hook failed",
						Error: azdext.WrapError(&azdext.ServiceError{
							Message:     "extension service hook failed",
							ErrorCode:   "Conflict",
							StatusCode:  409,
							ServiceName: "management.azure.com",
							Suggestion:  "Wait for the active operation to finish",
							Links: []errorhandler.ErrorLink{{
								URL:   "https://aka.ms/azd-errors#conflict",
								Title: "Conflict troubleshooting",
							}},
						}),
					},
				},
			}
		},
	)
	defer cleanup()

	handler := service.createServiceEventHandler(streamCtx, serviceConfig, extension, "prepackage", broker)
	err = handler(t.Context(), project.ServiceLifecycleEventArgs{
		Project:        projectConfig,
		Service:        serviceConfig,
		ServiceContext: project.NewServiceContext(),
	})
	require.Error(t, err)

	serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
	require.True(t, ok)
	assert.Equal(t, "extension service hook failed", serviceErr.Message)
	assert.Equal(t, "Conflict", serviceErr.ErrorCode)
	assert.Equal(t, 409, serviceErr.StatusCode)
	assert.Equal(t, "management.azure.com", serviceErr.ServiceName)
	assert.Equal(t, "Wait for the active operation to finish", serviceErr.Suggestion)
	require.Len(t, serviceErr.Links, 1)
	assert.Equal(t, "Conflict troubleshooting", serviceErr.Links[0].Title)
}

func TestEventService_createServiceEventHandler_BackCompatFailedMessage(t *testing.T) {
	service, _ := createTestEventService()
	extension := createTestExtension()
	projectConfig, err := service.lazyProject.GetValue()
	require.NoError(t, err)
	serviceConfig := projectConfig.Services["api"]
	require.NotNil(t, serviceConfig)

	broker, streamCtx, cleanup := createBrokerForEventHandler(
		t,
		extension.Id,
		func(msg *azdext.EventMessage) *azdext.EventMessage {
			invoke := msg.GetInvokeServiceHandler()
			require.NotNil(t, invoke)

			return &azdext.EventMessage{
				MessageType: &azdext.EventMessage_ServiceHandlerStatus{
					ServiceHandlerStatus: &azdext.ServiceHandlerStatus{
						EventName:   invoke.EventName,
						ServiceName: invoke.Service.Name,
						Status:      "failed",
						Message:     "old service host failure message",
					},
				},
			}
		},
	)
	defer cleanup()

	handler := service.createServiceEventHandler(streamCtx, serviceConfig, extension, "prepackage", broker)
	err = handler(t.Context(), project.ServiceLifecycleEventArgs{
		Project:        projectConfig,
		Service:        serviceConfig,
		ServiceContext: project.NewServiceContext(),
	})
	require.EqualError(
		t,
		err,
		"extension test.extension service hook api.prepackage failed: old service host failure message",
	)
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

func TestEventService_EmptyEventNameInArray(t *testing.T) {
	service, _ := createTestEventService()
	extension := createTestExtension()
	ctx := t.Context()

	t.Run("project_event_with_empty_name", func(t *testing.T) {
		var mockBroker *grpcbroker.MessageBroker[azdext.EventMessage]

		err := service.onSubscribeProjectEvent(ctx, extension, &azdext.SubscribeProjectEvent{
			EventNames: []string{"prepackage", "", "postpackage"},
		}, mockBroker)

		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.InvalidArgument, st.Code())
		require.Contains(t, st.Message(), "event name at index 1 cannot be empty")
	})

	t.Run("service_event_with_empty_name", func(t *testing.T) {
		var mockBroker *grpcbroker.MessageBroker[azdext.EventMessage]

		err := service.onSubscribeServiceEvent(ctx, extension, &azdext.SubscribeServiceEvent{
			EventNames: []string{"", "prepackage"},
		}, mockBroker)

		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.InvalidArgument, st.Code())
		require.Contains(t, st.Message(), "event name at index 0 cannot be empty")
	})
}
