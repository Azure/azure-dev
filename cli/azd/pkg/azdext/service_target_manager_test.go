// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockServiceTargetProvider implements ServiceTargetProvider using testify/mock
type MockServiceTargetProvider struct {
	mock.Mock
}

func (m *MockServiceTargetProvider) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	args := m.Called(ctx, serviceConfig)
	return args.Error(0)
}

func (m *MockServiceTargetProvider) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *TargetResource,
) ([]string, error) {
	args := m.Called(ctx, serviceConfig, targetResource)
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockServiceTargetProvider) GetTargetResource(
	ctx context.Context,
	subscriptionId string,
	serviceConfig *ServiceConfig,
	defaultResolver func() (*TargetResource, error),
) (*TargetResource, error) {
	args := m.Called(ctx, subscriptionId, serviceConfig, defaultResolver)
	return args.Get(0).(*TargetResource), args.Error(1)
}

func (m *MockServiceTargetProvider) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress ProgressReporter,
) (*ServicePackageResult, error) {
	args := m.Called(ctx, serviceConfig, serviceContext, progress)
	return args.Get(0).(*ServicePackageResult), args.Error(1)
}

func (m *MockServiceTargetProvider) Publish(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *TargetResource,
	publishOptions *PublishOptions,
	progress ProgressReporter,
) (*ServicePublishResult, error) {
	args := m.Called(ctx, serviceConfig, serviceContext, targetResource, publishOptions, progress)
	return args.Get(0).(*ServicePublishResult), args.Error(1)
}

func (m *MockServiceTargetProvider) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *TargetResource,
	progress ProgressReporter,
) (*ServiceDeployResult, error) {
	args := m.Called(ctx, serviceConfig, serviceContext, targetResource, progress)
	return args.Get(0).(*ServiceDeployResult), args.Error(1)
}

// Test helper functions
func createTestServiceTargetManager() *ServiceTargetManager {
	return &ServiceTargetManager{
		client:           nil, // Not needed for business logic tests
		stream:           nil, // Not needed for business logic tests
		componentManager: NewComponentManager[ServiceTargetProvider](ServiceTargetFactoryKey, "service target"),
	}
}

func createTestServiceConfigForServiceTarget(name, host string) *ServiceConfig {
	return &ServiceConfig{
		Name: name,
		Host: host,
	}
}

func TestNewServiceTargetManager(t *testing.T) {
	t.Parallel()

	mockClient := &AzdClient{} // Can be nil for this test
	manager := NewServiceTargetManager(mockClient)

	assert.NotNil(t, manager)
	assert.Equal(t, mockClient, manager.client)
	assert.NotNil(t, manager.componentManager)
	assert.Nil(t, manager.stream) // Stream should be nil until Register is called
}

func TestServiceTargetManager_FactoryRegistration(t *testing.T) {
	t.Parallel()

	manager := createTestServiceTargetManager()

	// Create a mock factory
	factory := func() ServiceTargetProvider {
		provider := &MockServiceTargetProvider{}
		provider.On("Initialize", mock.Anything, mock.Anything).Return(nil)
		return provider
	}

	// Register factory through component manager
	manager.componentManager.RegisterFactory("containerapp", factory)

	// Verify factory is registered
	assert.True(t, manager.componentManager.HasFactory("containerapp"))
	assert.False(t, manager.componentManager.HasFactory("webapp"))
}

func TestServiceTargetManager_InitializeRequest_Success(t *testing.T) {
	t.Parallel()

	manager := createTestServiceTargetManager()
	ctx := context.Background()

	// Set up mock provider
	mockProvider := &MockServiceTargetProvider{}
	mockProvider.On("Initialize", mock.Anything, mock.Anything).Return(nil)

	factory := func() ServiceTargetProvider {
		return mockProvider
	}
	manager.componentManager.RegisterFactory("containerapp", factory)

	// Create initialize request
	serviceConfig := createTestServiceConfigForServiceTarget("web-service", "containerapp")
	requestId := uuid.NewString()
	msg := &ServiceTargetMessage{
		RequestId: requestId,
		MessageType: &ServiceTargetMessage_InitializeRequest{
			InitializeRequest: &ServiceTargetInitializeRequest{
				ServiceConfig: serviceConfig,
			},
		},
	}

	// Execute
	resp := manager.buildServiceTargetResponseMsg(ctx, msg)

	// Verify response
	require.NotNil(t, resp)
	assert.Equal(t, requestId, resp.RequestId)
	assert.Nil(t, resp.Error)
	assert.NotNil(t, resp.GetInitializeResponse())

	// Verify mock expectations
	mockProvider.AssertExpectations(t)
}

func TestServiceTargetManager_InitializeRequest_NilServiceConfig(t *testing.T) {
	t.Parallel()

	manager := createTestServiceTargetManager()
	ctx := context.Background()

	requestId := uuid.NewString()
	msg := &ServiceTargetMessage{
		RequestId: requestId,
		MessageType: &ServiceTargetMessage_InitializeRequest{
			InitializeRequest: &ServiceTargetInitializeRequest{
				ServiceConfig: nil, // Nil service config
			},
		},
	}

	// Execute
	resp := manager.buildServiceTargetResponseMsg(ctx, msg)

	// Verify error response
	require.NotNil(t, resp)
	assert.Equal(t, requestId, resp.RequestId)
	assert.NotNil(t, resp.Error)
	assert.Contains(t, resp.Error.Message, "service config is required for initialize request")
	assert.Nil(t, resp.GetInitializeResponse())
}

func TestServiceTargetManager_InitializeRequest_ProviderInitializationError(t *testing.T) {
	t.Parallel()

	manager := createTestServiceTargetManager()
	ctx := context.Background()

	// Set up mock provider with initialization error
	expectedError := errors.New("initialization failed")
	mockProvider := &MockServiceTargetProvider{}
	mockProvider.On("Initialize", mock.Anything, mock.Anything).Return(expectedError)

	factory := func() ServiceTargetProvider {
		return mockProvider
	}
	manager.componentManager.RegisterFactory("containerapp", factory)

	// Create initialize request
	serviceConfig := createTestServiceConfigForServiceTarget("web-service", "containerapp")
	requestId := uuid.NewString()
	msg := &ServiceTargetMessage{
		RequestId: requestId,
		MessageType: &ServiceTargetMessage_InitializeRequest{
			InitializeRequest: &ServiceTargetInitializeRequest{
				ServiceConfig: serviceConfig,
			},
		},
	}

	// Execute
	resp := manager.buildServiceTargetResponseMsg(ctx, msg)

	// Verify error response
	require.NotNil(t, resp)
	assert.Equal(t, requestId, resp.RequestId)
	assert.NotNil(t, resp.Error)
	assert.Contains(t, resp.Error.Message, "initialization failed")
	assert.NotNil(t, resp.GetInitializeResponse()) // Response should still be created

	// Verify mock expectations
	mockProvider.AssertExpectations(t)
}

func TestServiceTargetManager_GetTargetResourceRequest_Success(t *testing.T) {
	t.Parallel()

	manager := createTestServiceTargetManager()
	ctx := context.Background()

	// Set up mock provider
	mockProvider := &MockServiceTargetProvider{}
	mockProvider.On("Initialize", mock.Anything, mock.Anything).Return(nil)

	expectedTargetResource := &TargetResource{
		ResourceName:      "test-app",
		ResourceType:      "Microsoft.App/containerApps",
		SubscriptionId:    "test-subscription",
		ResourceGroupName: "test-rg",
	}
	mockProvider.On("GetTargetResource", mock.Anything, "test-subscription", mock.Anything, mock.Anything).
		Return(expectedTargetResource, nil)

	factory := func() ServiceTargetProvider {
		return mockProvider
	}
	manager.componentManager.RegisterFactory("containerapp", factory)

	// Create and initialize instance first
	serviceConfig := createTestServiceConfigForServiceTarget("web-service", "containerapp")
	_, err := manager.componentManager.GetOrCreateInstance(ctx, serviceConfig)
	require.NoError(t, err)

	// Create get target resource request
	requestId := uuid.NewString()
	msg := &ServiceTargetMessage{
		RequestId: requestId,
		MessageType: &ServiceTargetMessage_GetTargetResourceRequest{
			GetTargetResourceRequest: &GetTargetResourceRequest{
				SubscriptionId:        "test-subscription",
				ServiceConfig:         serviceConfig,
				DefaultTargetResource: nil,
				DefaultError:          "",
			},
		},
	}

	// Execute
	resp := manager.buildServiceTargetResponseMsg(ctx, msg)

	// Verify response
	require.NotNil(t, resp)
	assert.Equal(t, requestId, resp.RequestId)
	assert.Nil(t, resp.Error)
	assert.NotNil(t, resp.GetGetTargetResourceResponse())
	assert.Equal(t, expectedTargetResource, resp.GetGetTargetResourceResponse().TargetResource)

	// Verify mock expectations
	mockProvider.AssertExpectations(t)
}

func TestServiceTargetManager_GetTargetResourceRequest_NoProvider(t *testing.T) {
	t.Parallel()

	manager := createTestServiceTargetManager()
	ctx := context.Background()

	// Create request without initializing any provider
	serviceConfig := createTestServiceConfigForServiceTarget("web-service", "containerapp")
	requestId := uuid.NewString()
	msg := &ServiceTargetMessage{
		RequestId: requestId,
		MessageType: &ServiceTargetMessage_GetTargetResourceRequest{
			GetTargetResourceRequest: &GetTargetResourceRequest{
				SubscriptionId:        "test-subscription",
				ServiceConfig:         serviceConfig,
				DefaultTargetResource: nil,
				DefaultError:          "",
			},
		},
	}

	// Execute
	resp := manager.buildServiceTargetResponseMsg(ctx, msg)

	// Verify error response
	require.NotNil(t, resp)
	assert.Equal(t, requestId, resp.RequestId)
	assert.NotNil(t, resp.Error)
	assert.Contains(t, resp.Error.Message, "no provider instance found for service: web-service")
	assert.Contains(t, resp.Error.Message, "Initialize must be called first")
	assert.Nil(t, resp.GetGetTargetResourceResponse())
}

func TestServiceTargetManager_EndpointsRequest_Success(t *testing.T) {
	t.Parallel()

	manager := createTestServiceTargetManager()
	ctx := context.Background()

	// Set up mock provider
	mockProvider := &MockServiceTargetProvider{}
	mockProvider.On("Initialize", mock.Anything, mock.Anything).Return(nil)

	expectedEndpoints := []string{"https://test-app.azurecontainerapps.io", "https://test-app-custom.com"}
	targetResource := &TargetResource{
		ResourceName:      "test-app",
		ResourceType:      "Microsoft.App/containerApps",
		SubscriptionId:    "test-subscription",
		ResourceGroupName: "test-rg",
	}
	mockProvider.On("Endpoints", mock.Anything, mock.Anything, targetResource).
		Return(expectedEndpoints, nil)

	factory := func() ServiceTargetProvider {
		return mockProvider
	}
	manager.componentManager.RegisterFactory("containerapp", factory)

	// Create and initialize instance first
	serviceConfig := createTestServiceConfigForServiceTarget("web-service", "containerapp")
	_, err := manager.componentManager.GetOrCreateInstance(ctx, serviceConfig)
	require.NoError(t, err)

	// Create endpoints request
	requestId := uuid.NewString()
	msg := &ServiceTargetMessage{
		RequestId: requestId,
		MessageType: &ServiceTargetMessage_EndpointsRequest{
			EndpointsRequest: &ServiceTargetEndpointsRequest{
				ServiceConfig:  serviceConfig,
				TargetResource: targetResource,
			},
		},
	}

	// Execute
	resp := manager.buildServiceTargetResponseMsg(ctx, msg)

	// Verify response
	require.NotNil(t, resp)
	assert.Equal(t, requestId, resp.RequestId)
	assert.Nil(t, resp.Error)
	assert.NotNil(t, resp.GetEndpointsResponse())
	assert.Equal(t, expectedEndpoints, resp.GetEndpointsResponse().Endpoints)

	// Verify mock expectations
	mockProvider.AssertExpectations(t)
}

func TestServiceTargetManager_PackageRequest_NilServiceConfig(t *testing.T) {
	t.Parallel()

	manager := createTestServiceTargetManager()
	ctx := context.Background()

	requestId := uuid.NewString()
	msg := &ServiceTargetMessage{
		RequestId: requestId,
		MessageType: &ServiceTargetMessage_PackageRequest{
			PackageRequest: &ServiceTargetPackageRequest{
				ServiceConfig:  nil, // Nil service config
				ServiceContext: &ServiceContext{},
			},
		},
	}

	// Execute
	resp := manager.buildServiceTargetResponseMsg(ctx, msg)

	// Verify error response
	require.NotNil(t, resp)
	assert.Equal(t, requestId, resp.RequestId)
	assert.NotNil(t, resp.Error)
	assert.Contains(t, resp.Error.Message, "service config is required for package request")
	assert.Nil(t, resp.GetPackageResponse())
}

func TestServiceTargetManager_PublishRequest_NilServiceConfig(t *testing.T) {
	t.Parallel()

	manager := createTestServiceTargetManager()
	ctx := context.Background()

	requestId := uuid.NewString()
	msg := &ServiceTargetMessage{
		RequestId: requestId,
		MessageType: &ServiceTargetMessage_PublishRequest{
			PublishRequest: &ServiceTargetPublishRequest{
				ServiceConfig:  nil, // Nil service config
				ServiceContext: &ServiceContext{},
				TargetResource: &TargetResource{},
				PublishOptions: &PublishOptions{},
			},
		},
	}

	// Execute
	resp := manager.buildServiceTargetResponseMsg(ctx, msg)

	// Verify error response
	require.NotNil(t, resp)
	assert.Equal(t, requestId, resp.RequestId)
	assert.NotNil(t, resp.Error)
	assert.Contains(t, resp.Error.Message, "service config is required for publish request")
	assert.Nil(t, resp.GetPublishResponse())
}

func TestServiceTargetManager_DeployRequest_NilServiceConfig(t *testing.T) {
	t.Parallel()

	manager := createTestServiceTargetManager()
	ctx := context.Background()

	requestId := uuid.NewString()
	msg := &ServiceTargetMessage{
		RequestId: requestId,
		MessageType: &ServiceTargetMessage_DeployRequest{
			DeployRequest: &ServiceTargetDeployRequest{
				ServiceConfig:  nil, // Nil service config
				ServiceContext: &ServiceContext{},
				TargetResource: &TargetResource{},
			},
		},
	}

	// Execute
	resp := manager.buildServiceTargetResponseMsg(ctx, msg)

	// Verify error response
	require.NotNil(t, resp)
	assert.Equal(t, requestId, resp.RequestId)
	assert.NotNil(t, resp.Error)
	assert.Contains(t, resp.Error.Message, "service config is required for deploy request")
	assert.Nil(t, resp.GetDeployResponse())
}

func TestServiceTargetManager_UnknownMessageType(t *testing.T) {
	t.Parallel()

	manager := createTestServiceTargetManager()
	ctx := context.Background()

	// Create message with unknown/unhandled type
	requestId := uuid.NewString()
	msg := &ServiceTargetMessage{
		RequestId:   requestId,
		MessageType: nil, // Unknown message type
	}

	// Execute
	resp := manager.buildServiceTargetResponseMsg(ctx, msg)

	// Should return nil for unknown message types
	assert.Nil(t, resp)
}

func TestServiceTargetManager_Close_ComponentManagerIntegration(t *testing.T) {
	t.Parallel()

	manager := createTestServiceTargetManager()

	// Register a factory and create instance
	mockProvider := &MockServiceTargetProvider{}
	mockProvider.On("Initialize", mock.Anything, mock.Anything).Return(nil)

	factory := func() ServiceTargetProvider {
		return mockProvider
	}
	manager.componentManager.RegisterFactory("containerapp", factory)

	ctx := context.Background()
	serviceConfig := createTestServiceConfigForServiceTarget("web-service", "containerapp")
	_, err := manager.componentManager.GetOrCreateInstance(ctx, serviceConfig)
	require.NoError(t, err)

	// Verify instance exists
	instance, err := manager.componentManager.GetInstance("web-service")
	assert.NoError(t, err)
	assert.NotNil(t, instance)

	// Close the manager
	err = manager.Close()
	assert.NoError(t, err)

	// Verify component manager instances are cleared
	instance, err = manager.componentManager.GetInstance("web-service")
	assert.Error(t, err)
	assert.Nil(t, instance)
}

func TestServiceTargetManager_MultipleRequestTypes_SameProvider(t *testing.T) {
	t.Parallel()

	manager := createTestServiceTargetManager()
	ctx := context.Background()

	// Set up mock provider with multiple method expectations
	mockProvider := &MockServiceTargetProvider{}
	mockProvider.On("Initialize", mock.Anything, mock.Anything).Return(nil)

	expectedEndpoints := []string{"https://test-app.azurecontainerapps.io"}
	targetResource := &TargetResource{
		ResourceName:      "test-app",
		ResourceType:      "Microsoft.App/containerApps",
		SubscriptionId:    "test-subscription",
		ResourceGroupName: "test-rg",
	}
	mockProvider.On("Endpoints", mock.Anything, mock.Anything, targetResource).
		Return(expectedEndpoints, nil)

	expectedTargetResource := &TargetResource{
		ResourceName:      "test-app",
		ResourceType:      "Microsoft.App/containerApps",
		SubscriptionId:    "test-subscription",
		ResourceGroupName: "test-rg",
	}
	mockProvider.On("GetTargetResource", mock.Anything, "test-subscription", mock.Anything, mock.Anything).
		Return(expectedTargetResource, nil)

	factory := func() ServiceTargetProvider {
		return mockProvider
	}
	manager.componentManager.RegisterFactory("containerapp", factory)

	serviceConfig := createTestServiceConfigForServiceTarget("web-service", "containerapp")

	// Initialize first
	initMsg := &ServiceTargetMessage{
		RequestId: uuid.NewString(),
		MessageType: &ServiceTargetMessage_InitializeRequest{
			InitializeRequest: &ServiceTargetInitializeRequest{
				ServiceConfig: serviceConfig,
			},
		},
	}
	initResp := manager.buildServiceTargetResponseMsg(ctx, initMsg)
	require.NotNil(t, initResp)
	assert.Nil(t, initResp.Error)

	// Test endpoints request
	endpointsMsg := &ServiceTargetMessage{
		RequestId: uuid.NewString(),
		MessageType: &ServiceTargetMessage_EndpointsRequest{
			EndpointsRequest: &ServiceTargetEndpointsRequest{
				ServiceConfig:  serviceConfig,
				TargetResource: targetResource,
			},
		},
	}
	endpointsResp := manager.buildServiceTargetResponseMsg(ctx, endpointsMsg)
	require.NotNil(t, endpointsResp)
	assert.Nil(t, endpointsResp.Error)
	assert.Equal(t, expectedEndpoints, endpointsResp.GetEndpointsResponse().Endpoints)

	// Test get target resource request
	getTargetMsg := &ServiceTargetMessage{
		RequestId: uuid.NewString(),
		MessageType: &ServiceTargetMessage_GetTargetResourceRequest{
			GetTargetResourceRequest: &GetTargetResourceRequest{
				SubscriptionId:        "test-subscription",
				ServiceConfig:         serviceConfig,
				DefaultTargetResource: nil,
				DefaultError:          "",
			},
		},
	}
	getTargetResp := manager.buildServiceTargetResponseMsg(ctx, getTargetMsg)
	require.NotNil(t, getTargetResp)
	assert.Nil(t, getTargetResp.Error)
	assert.Equal(t, expectedTargetResource, getTargetResp.GetGetTargetResourceResponse().TargetResource)

	// Verify all mock expectations
	mockProvider.AssertExpectations(t)
}
