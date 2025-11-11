// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// MockExtensionServiceClient implements ExtensionServiceClient using testify/mock
type MockExtensionServiceClient struct {
	mock.Mock
}

func (m *MockExtensionServiceClient) Ready(
	ctx context.Context,
	in *ReadyRequest,
	opts ...grpc.CallOption,
) (*ReadyResponse, error) {
	args := m.Called(ctx, in, opts)
	return args.Get(0).(*ReadyResponse), args.Error(1)
}

// MockServiceTargetRegistrar implements serviceTargetRegistrar using testify/mock
type MockServiceTargetRegistrar struct {
	mock.Mock
}

func (m *MockServiceTargetRegistrar) Register(ctx context.Context, factory ServiceTargetFactory, hostType string) error {
	args := m.Called(ctx, factory, hostType)
	return args.Error(0)
}

func (m *MockServiceTargetRegistrar) Receive(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockServiceTargetRegistrar) Ready(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockServiceTargetRegistrar) Close() error {
	args := m.Called()
	return args.Error(0)
}

// MockFrameworkServiceRegistrar implements frameworkServiceRegistrar using testify/mock
type MockFrameworkServiceRegistrar struct {
	mock.Mock
}

func (m *MockFrameworkServiceRegistrar) Register(
	ctx context.Context,
	factory FrameworkServiceFactory,
	language string,
) error {
	args := m.Called(ctx, factory, language)
	return args.Error(0)
}

func (m *MockFrameworkServiceRegistrar) Receive(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockFrameworkServiceRegistrar) Ready(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockFrameworkServiceRegistrar) Close() error {
	args := m.Called()
	return args.Error(0)
}

// MockExtensionEventManager implements extensionEventManager using testify/mock
type MockExtensionEventManager struct {
	mock.Mock
}

func (m *MockExtensionEventManager) AddProjectEventHandler(
	ctx context.Context,
	eventName string,
	handler ProjectEventHandler,
) error {
	args := m.Called(ctx, eventName, handler)
	return args.Error(0)
}

func (m *MockExtensionEventManager) AddServiceEventHandler(
	ctx context.Context,
	eventName string,
	handler ServiceEventHandler,
	options *ServiceEventOptions,
) error {
	args := m.Called(ctx, eventName, handler, options)
	return args.Error(0)
}

func (m *MockExtensionEventManager) Receive(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockExtensionEventManager) Ready(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockExtensionEventManager) Close() error {
	args := m.Called()
	return args.Error(0)
}

func TestCallReady(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		setupMock      func(*MockExtensionServiceClient)
		wantErr        bool
		expectedErrMsg string
	}{
		{
			name: "success",
			setupMock: func(mockClient *MockExtensionServiceClient) {
				mockClient.On("Ready", mock.Anything, mock.Anything, mock.Anything).
					Return(&ReadyResponse{}, nil)
			},
			wantErr: false,
		},
		{
			name: "canceled - no error",
			setupMock: func(mockClient *MockExtensionServiceClient) {
				mockClient.On("Ready", mock.Anything, mock.Anything, mock.Anything).
					Return((*ReadyResponse)(nil), status.Error(codes.Canceled, "ctx cancelled"))
			},
			wantErr: false,
		},
		{
			name: "unavailable - no error",
			setupMock: func(mockClient *MockExtensionServiceClient) {
				mockClient.On("Ready", mock.Anything, mock.Anything, mock.Anything).
					Return((*ReadyResponse)(nil), status.Error(codes.Unavailable, "shutdown"))
			},
			wantErr: false,
		},
		{
			name: "internal error",
			setupMock: func(mockClient *MockExtensionServiceClient) {
				mockClient.On("Ready", mock.Anything, mock.Anything, mock.Anything).
					Return((*ReadyResponse)(nil), status.Error(codes.Internal, "boom"))
			},
			wantErr:        true,
			expectedErrMsg: "status=Internal",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockClient := &MockExtensionServiceClient{}
			tt.setupMock(mockClient)

			client := &AzdClient{extensionClient: mockClient}
			err := callReady(context.Background(), client)

			if tt.wantErr {
				require.Error(t, err)
				if tt.expectedErrMsg != "" {
					assert.Contains(t, err.Error(), tt.expectedErrMsg)
				}
			} else {
				require.NoError(t, err)
			}

			mockClient.AssertExpectations(t)
		})
	}
}

// newTestAzdClient creates an AzdClient with mock extension service that returns success for Ready()
func newTestAzdClient() *AzdClient {
	mockExtensionClient := &MockExtensionServiceClient{}
	mockExtensionClient.On("Ready", mock.Anything, mock.Anything, mock.Anything).
		Return(&ReadyResponse{}, nil)
	return &AzdClient{extensionClient: mockExtensionClient}
}

func TestExtensionHost_ServiceTargetOnly(t *testing.T) {
	t.Parallel()

	// Setup mocks
	mockServiceTargetManager := &MockServiceTargetRegistrar{}
	registrationComplete := make(chan struct{})
	mockServiceTargetManager.On("Register", mock.Anything, mock.Anything, mock.AnythingOfType("string")).
		Run(func(args mock.Arguments) {
			close(registrationComplete)
		}).
		Return(nil)
	// Mock Ready to return immediately and Receive to block until context is cancelled
	mockServiceTargetManager.On("Ready", mock.Anything).Return(nil)
	mockServiceTargetManager.On("Receive", mock.Anything).Run(func(args mock.Arguments) {
		ctx := args.Get(0).(context.Context)
		<-ctx.Done()
	}).Return(nil)
	mockServiceTargetManager.On("Close").Return(nil)

	// Setup extension host
	client := newTestAzdClient()
	runner := NewExtensionHost(client)
	runner.serviceTargetManager = mockServiceTargetManager

	// Register service target
	runner.WithServiceTarget("demo", func() ServiceTargetProvider {
		// Return the reusable MockServiceTargetProvider
		return &MockServiceTargetProvider{}
	})

	// Run test
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-registrationComplete // Wait for registration to complete
		cancel()
	}()

	err := runner.Run(ctx)

	// Assertions
	require.NoError(t, err)
	mockServiceTargetManager.AssertExpectations(t)
}

func TestExtensionHost_EventHandlersOnly(t *testing.T) {
	t.Parallel()

	// Setup mocks
	mockEventManager := &MockExtensionEventManager{}
	var wg sync.WaitGroup
	wg.Add(2) // We expect 2 registrations
	registrationComplete := make(chan struct{})
	go func() {
		wg.Wait()
		close(registrationComplete)
	}()
	mockEventManager.On("AddProjectEventHandler", mock.Anything, "preprovision", mock.Anything).
		Run(func(args mock.Arguments) {
			wg.Done()
		}).
		Return(nil)
	mockEventManager.On("AddServiceEventHandler", mock.Anything, "prepackage", mock.Anything,
		(*ServiceEventOptions)(nil)).Run(func(args mock.Arguments) {
		wg.Done()
	}).Return(nil)
	// Mock Ready to return immediately and Receive to block until context is cancelled
	mockEventManager.On("Ready", mock.Anything).Return(nil)
	mockEventManager.On("Receive", mock.Anything).Run(func(args mock.Arguments) {
		ctx := args.Get(0).(context.Context)
		<-ctx.Done()
	}).Return(nil)
	mockEventManager.On("Close").Return(nil)

	// Setup extension host
	client := newTestAzdClient()
	runner := NewExtensionHost(client)
	runner.eventManager = mockEventManager

	// Register event handlers
	runner.WithProjectEventHandler("preprovision", func(ctx context.Context, args *ProjectEventArgs) error {
		return nil
	})
	runner.WithServiceEventHandler("prepackage", func(ctx context.Context, args *ServiceEventArgs) error {
		return nil
	}, nil)

	// Run test
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-registrationComplete // Wait for all registrations to complete
		cancel()
	}()

	err := runner.Run(ctx)

	// Assertions
	require.NoError(t, err)
	mockEventManager.AssertExpectations(t)
}

func TestExtensionHost_ServiceTargetsAndEvents(t *testing.T) {
	t.Parallel()

	// Setup mocks
	mockServiceTargetManager := &MockServiceTargetRegistrar{}
	var wg sync.WaitGroup
	wg.Add(2) // We expect 2 registrations total
	registrationComplete := make(chan struct{})
	go func() {
		wg.Wait()
		close(registrationComplete)
	}()
	mockServiceTargetManager.On("Register", mock.Anything, mock.Anything, mock.AnythingOfType("string")).
		Run(func(args mock.Arguments) {
			wg.Done()
		}).
		Return(nil)
	// Mock Ready and Receive to support new interface
	mockServiceTargetManager.On("Ready", mock.Anything).Return(nil)
	mockServiceTargetManager.On("Receive", mock.Anything).Run(func(args mock.Arguments) {
		ctx := args.Get(0).(context.Context)
		<-ctx.Done()
	}).Return(nil)
	mockServiceTargetManager.On("Close").Return(nil)

	mockEventManager := &MockExtensionEventManager{}
	mockEventManager.On("AddProjectEventHandler", mock.Anything, "preprovision", mock.Anything).
		Run(func(args mock.Arguments) {
			wg.Done()
		}).
		Return(nil)
	// Mock Ready and Receive to support new interface
	mockEventManager.On("Ready", mock.Anything).Return(nil)
	mockEventManager.On("Receive", mock.Anything).Run(func(args mock.Arguments) {
		ctx := args.Get(0).(context.Context)
		<-ctx.Done()
	}).Return(nil)
	mockEventManager.On("Close").Return(nil)

	// Setup extension host
	client := newTestAzdClient()
	runner := NewExtensionHost(client)
	runner.serviceTargetManager = mockServiceTargetManager
	runner.eventManager = mockEventManager

	// Register both service target and event handler
	runner.WithServiceTarget("foundryagent", func() ServiceTargetProvider {
		return &MockServiceTargetProvider{}
	})
	runner.WithProjectEventHandler("preprovision", func(ctx context.Context, args *ProjectEventArgs) error {
		return nil
	})

	// Run test
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-registrationComplete // Wait for all registrations to complete
		cancel()
	}()

	err := runner.Run(ctx)

	// Assertions
	require.NoError(t, err)
	mockServiceTargetManager.AssertExpectations(t)
	mockEventManager.AssertExpectations(t)
}

func TestExtensionHost_ServiceTargetRegistrationError(t *testing.T) {
	t.Parallel()

	// Setup mock with error
	expectedError := status.Error(codes.Internal, "boom")
	mockServiceTargetManager := &MockServiceTargetRegistrar{}

	// Use a channel to ensure Receive() goroutine has started before we proceed
	receiveStarted := make(chan struct{})
	mockServiceTargetManager.On("Ready", mock.Anything).Return(nil)
	mockServiceTargetManager.On("Receive", mock.Anything).Run(func(args mock.Arguments) {
		ctx := args.Get(0).(context.Context)
		close(receiveStarted) // Signal that Receive has been called
		<-ctx.Done()
	}).Return(nil)

	mockServiceTargetManager.
		On("Register", mock.Anything, mock.Anything, mock.AnythingOfType("string")).
		Run(func(args mock.Arguments) {
			// Wait for Receive to start before Register proceeds
			<-receiveStarted
		}).Return(expectedError)

	mockServiceTargetManager.On("Close").Return(nil)

	// Setup extension host
	client := newTestAzdClient()
	runner := NewExtensionHost(client)
	runner.serviceTargetManager = mockServiceTargetManager

	// Register service target that will fail
	runner.WithServiceTarget("bad", func() ServiceTargetProvider {
		return &MockServiceTargetProvider{}
	})

	// Run test
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := runner.Run(ctx)

	// Assertions
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to register service target")
	mockServiceTargetManager.AssertExpectations(t)
}

func TestExtensionHost_WithFrameworkService(t *testing.T) {
	t.Parallel()

	// Setup mocks
	mockFrameworkServiceManager := &MockFrameworkServiceRegistrar{}
	registrationComplete := make(chan struct{})
	mockFrameworkServiceManager.On("Register", mock.Anything, mock.Anything, mock.AnythingOfType("string")).
		Run(func(args mock.Arguments) {
			close(registrationComplete)
		}).
		Return(nil)
	// Mock Ready and Receive to support new interface
	mockFrameworkServiceManager.On("Ready", mock.Anything).Return(nil)
	mockFrameworkServiceManager.On("Receive", mock.Anything).Run(func(args mock.Arguments) {
		ctx := args.Get(0).(context.Context)
		<-ctx.Done()
	}).Return(nil)
	mockFrameworkServiceManager.On("Close").Return(nil)

	// Setup extension host
	client := newTestAzdClient()
	runner := NewExtensionHost(client)
	runner.frameworkServiceManager = mockFrameworkServiceManager

	// Register framework service
	runner.WithFrameworkService("python", func() FrameworkServiceProvider {
		// Reuse mock from framework_service_manager_test.go
		return &MockFrameworkServiceProvider{}
	})

	// Run test
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-registrationComplete // Wait for registration to complete
		cancel()
	}()

	err := runner.Run(ctx)

	// Assertions
	require.NoError(t, err)
	mockFrameworkServiceManager.AssertExpectations(t)
}

func TestExtensionHost_MultipleServiceTypes(t *testing.T) {
	t.Parallel()

	// Setup mocks for all service types
	var wg sync.WaitGroup
	wg.Add(3) // We expect 3 registrations total
	registrationComplete := make(chan struct{})
	go func() {
		wg.Wait()
		close(registrationComplete)
	}()

	mockServiceTargetManager := &MockServiceTargetRegistrar{}
	mockServiceTargetManager.On("Register", mock.Anything, mock.Anything, mock.AnythingOfType("string")).
		Run(func(args mock.Arguments) {
			wg.Done()
		}).Return(nil)
	// Mock Ready and Receive to support new interface
	mockServiceTargetManager.On("Ready", mock.Anything).Return(nil)
	mockServiceTargetManager.On("Receive", mock.Anything).Run(func(args mock.Arguments) {
		ctx := args.Get(0).(context.Context)
		<-ctx.Done()
	}).Return(nil)
	mockServiceTargetManager.On("Close").Return(nil)

	mockFrameworkServiceManager := &MockFrameworkServiceRegistrar{}
	mockFrameworkServiceManager.On("Register", mock.Anything, mock.Anything, mock.AnythingOfType("string")).
		Run(func(args mock.Arguments) {
			wg.Done()
		}).
		Return(nil)
	// Mock Ready and Receive to support new interface
	mockFrameworkServiceManager.On("Ready", mock.Anything).Return(nil)
	mockFrameworkServiceManager.On("Receive", mock.Anything).Run(func(args mock.Arguments) {
		ctx := args.Get(0).(context.Context)
		<-ctx.Done()
	}).Return(nil)
	mockFrameworkServiceManager.On("Close").Return(nil)

	mockEventManager := &MockExtensionEventManager{}
	mockEventManager.On("AddProjectEventHandler", mock.Anything, "predeploy", mock.Anything).Run(func(args mock.Arguments) {
		wg.Done()
	}).Return(nil)
	// Mock Ready and Receive to support new interface
	mockEventManager.On("Ready", mock.Anything).Return(nil)
	mockEventManager.On("Receive", mock.Anything).Run(func(args mock.Arguments) {
		ctx := args.Get(0).(context.Context)
		<-ctx.Done()
	}).Return(nil)
	mockEventManager.On("Close").Return(nil)

	// Setup extension host
	client := newTestAzdClient()
	runner := NewExtensionHost(client)
	runner.serviceTargetManager = mockServiceTargetManager
	runner.frameworkServiceManager = mockFrameworkServiceManager
	runner.eventManager = mockEventManager

	// Register all service types
	runner.WithServiceTarget("web", func() ServiceTargetProvider {
		return &MockServiceTargetProvider{}
	})
	runner.WithFrameworkService("node", func() FrameworkServiceProvider {
		return &MockFrameworkServiceProvider{}
	})
	runner.WithProjectEventHandler("predeploy", func(ctx context.Context, args *ProjectEventArgs) error {
		return nil
	})

	// Run test
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-registrationComplete // Wait for all registrations to complete
		cancel()
	}()

	err := runner.Run(ctx)

	// Assertions
	require.NoError(t, err)
	mockServiceTargetManager.AssertExpectations(t)
	mockFrameworkServiceManager.AssertExpectations(t)
	mockEventManager.AssertExpectations(t)
}

func TestExtensionHost_MultipleRegistrationErrors(t *testing.T) {
	t.Parallel()

	// Setup mocks with errors
	error1 := status.Error(codes.Internal, "service target error")
	error2 := status.Error(codes.Internal, "framework service error")

	mockServiceTargetManager := &MockServiceTargetRegistrar{}
	mockServiceTargetManager.On("Receive", mock.Anything).Run(func(args mock.Arguments) {
		ctx := args.Get(0).(context.Context)
		<-ctx.Done()
	}).Return(nil)
	mockServiceTargetManager.On("Ready", mock.Anything).Return(nil)
	mockServiceTargetManager.On("Register", mock.Anything, mock.Anything, mock.AnythingOfType("string")).Return(error1)
	mockServiceTargetManager.On("Close").Return(nil)

	mockFrameworkServiceManager := &MockFrameworkServiceRegistrar{}
	mockFrameworkServiceManager.On("Receive", mock.Anything).Run(func(args mock.Arguments) {
		ctx := args.Get(0).(context.Context)
		<-ctx.Done()
	}).Return(nil)
	mockFrameworkServiceManager.On("Ready", mock.Anything).Return(nil)
	mockFrameworkServiceManager.On("Register", mock.Anything, mock.Anything, mock.AnythingOfType("string")).Return(error2)
	mockFrameworkServiceManager.On("Close").Return(nil)

	// Setup extension host
	client := newTestAzdClient()
	runner := NewExtensionHost(client)
	runner.serviceTargetManager = mockServiceTargetManager
	runner.frameworkServiceManager = mockFrameworkServiceManager

	// Register multiple failing services
	runner.WithServiceTarget("bad1", func() ServiceTargetProvider {
		return &MockServiceTargetProvider{}
	})
	runner.WithFrameworkService("bad2", func() FrameworkServiceProvider {
		return &MockFrameworkServiceProvider{}
	})

	// Run test
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// For error tests, we can use a timeout since registrations should fail quickly
	ctx, timeoutCancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer timeoutCancel()

	err := runner.Run(ctx)

	// Assertions - should return a joined error containing both original errors
	require.Error(t, err)

	// Test that errors.Join() was used properly - we can unwrap to find original errors
	require.True(t, errors.Is(err, error1), "should contain the service target error")
	require.True(t, errors.Is(err, error2), "should contain the framework service error")

	// Test error message contains both error messages
	require.Contains(t, err.Error(), "service target error")
	require.Contains(t, err.Error(), "framework service error")

	mockServiceTargetManager.AssertExpectations(t)
	mockFrameworkServiceManager.AssertExpectations(t)
}
