// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
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

func TestExtensionHost_ServiceTargetOnly(t *testing.T) {
	t.Parallel()

	// Setup mocks
	mockServiceTargetManager := &MockServiceTargetRegistrar{}
	mockServiceTargetManager.On("Register", mock.Anything, mock.Anything, "demo").Return(nil)
	mockServiceTargetManager.On("Close").Return(nil)

	// Setup extension host
	client := &AzdClient{}
	runner := NewExtensionHost(client)
	runner.newServiceTargetManager = func(*AzdClient) serviceTargetRegistrar {
		return mockServiceTargetManager
	}

	readyCalled := false
	runner.readyFn = func(ctx context.Context) error {
		readyCalled = true
		<-ctx.Done()
		return nil
	}

	// Register service target
	runner.WithServiceTarget("demo", func() ServiceTargetProvider {
		// Return the reusable MockServiceTargetProvider
		return &MockServiceTargetProvider{}
	})

	// Run test
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	err := runner.Run(ctx)

	// Assertions
	require.NoError(t, err)
	assert.True(t, readyCalled)
	mockServiceTargetManager.AssertExpectations(t)
}

func TestExtensionHost_EventHandlersOnly(t *testing.T) {
	t.Parallel()

	// Setup mocks
	mockEventManager := &MockExtensionEventManager{}
	mockEventManager.On("AddProjectEventHandler", mock.Anything, "preprovision", mock.Anything).Return(nil)
	mockEventManager.On("AddServiceEventHandler", mock.Anything, "prepackage", mock.Anything,
		(*ServiceEventOptions)(nil)).Return(nil)
	mockEventManager.On("Receive", mock.Anything).Return(nil)
	mockEventManager.On("Close").Return(nil)

	// Setup extension host
	client := &AzdClient{}
	runner := NewExtensionHost(client)
	runner.newEventManager = func(*AzdClient) extensionEventManager {
		return mockEventManager
	}
	runner.readyFn = func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	}

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
		time.Sleep(10 * time.Millisecond)
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
	mockServiceTargetManager.On("Register", mock.Anything, mock.Anything, "foundryagent").Return(nil)
	mockServiceTargetManager.On("Close").Return(nil)

	mockEventManager := &MockExtensionEventManager{}
	mockEventManager.On("AddProjectEventHandler", mock.Anything, "preprovision", mock.Anything).Return(nil)
	mockEventManager.On("Receive", mock.Anything).Return(nil)
	mockEventManager.On("Close").Return(nil)

	// Setup extension host
	client := &AzdClient{}
	runner := NewExtensionHost(client)
	runner.newServiceTargetManager = func(*AzdClient) serviceTargetRegistrar {
		return mockServiceTargetManager
	}
	runner.newEventManager = func(*AzdClient) extensionEventManager {
		return mockEventManager
	}
	runner.readyFn = func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	}

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
		time.Sleep(10 * time.Millisecond)
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
	mockServiceTargetManager.On("Register", mock.Anything, mock.Anything, "bad").Return(expectedError)
	mockServiceTargetManager.On("Close").Return(nil)

	// Setup extension host
	client := &AzdClient{}
	runner := NewExtensionHost(client)
	runner.newServiceTargetManager = func(*AzdClient) serviceTargetRegistrar {
		return mockServiceTargetManager
	}
	runner.readyFn = func(ctx context.Context) error {
		return nil
	}

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
	mockFrameworkServiceManager.On("Register", mock.Anything, mock.Anything, "python").Return(nil)
	mockFrameworkServiceManager.On("Close").Return(nil)

	// Setup extension host
	client := &AzdClient{}
	runner := NewExtensionHost(client)
	runner.newFrameworkServiceManager = func(*AzdClient) frameworkServiceRegistrar {
		return mockFrameworkServiceManager
	}

	readyCalled := false
	runner.readyFn = func(ctx context.Context) error {
		readyCalled = true
		<-ctx.Done()
		return nil
	}

	// Register framework service
	runner.WithFrameworkService("python", func() FrameworkServiceProvider {
		// Reuse mock from framework_service_manager_test.go
		return &MockFrameworkServiceProvider{}
	})

	// Run test
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	err := runner.Run(ctx)

	// Assertions
	require.NoError(t, err)
	assert.True(t, readyCalled)
	mockFrameworkServiceManager.AssertExpectations(t)
}

func TestExtensionHost_MultipleServiceTypes(t *testing.T) {
	t.Parallel()

	// Setup mocks for all service types
	mockServiceTargetManager := &MockServiceTargetRegistrar{}
	mockServiceTargetManager.On("Register", mock.Anything, mock.Anything, "web").Return(nil)
	mockServiceTargetManager.On("Close").Return(nil)

	mockFrameworkServiceManager := &MockFrameworkServiceRegistrar{}
	mockFrameworkServiceManager.On("Register", mock.Anything, mock.Anything, "node").Return(nil)
	mockFrameworkServiceManager.On("Close").Return(nil)

	mockEventManager := &MockExtensionEventManager{}
	mockEventManager.On("AddProjectEventHandler", mock.Anything, "predeploy", mock.Anything).Return(nil)
	mockEventManager.On("Receive", mock.Anything).Return(nil)
	mockEventManager.On("Close").Return(nil)

	// Setup extension host
	client := &AzdClient{}
	runner := NewExtensionHost(client)
	runner.newServiceTargetManager = func(*AzdClient) serviceTargetRegistrar {
		return mockServiceTargetManager
	}
	runner.newFrameworkServiceManager = func(*AzdClient) frameworkServiceRegistrar {
		return mockFrameworkServiceManager
	}
	runner.newEventManager = func(*AzdClient) extensionEventManager {
		return mockEventManager
	}
	runner.readyFn = func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	}

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
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	err := runner.Run(ctx)

	// Assertions
	require.NoError(t, err)
	mockServiceTargetManager.AssertExpectations(t)
	mockFrameworkServiceManager.AssertExpectations(t)
	mockEventManager.AssertExpectations(t)
}
