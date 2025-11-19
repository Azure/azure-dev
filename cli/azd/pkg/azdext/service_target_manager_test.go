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
) (*azdext.ServicePackageResult, error) {
	args := m.Called(ctx, serviceConfig, serviceContext, progress)
	return args.Get(0).(*azdext.ServicePackageResult), args.Error(1)
}

func (m *MockServiceTargetProvider) Publish(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *TargetResource,
	publishOptions *azdext.PublishOptions,
	progress ProgressReporter,
) (*azdext.ServicePublishResult, error) {
	args := m.Called(ctx, serviceConfig, serviceContext, targetResource, publishOptions, progress)
	return args.Get(0).(*azdext.ServicePublishResult), args.Error(1)
}

func (m *MockServiceTargetProvider) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *TargetResource,
	progress ProgressReporter,
) (*azdext.ServiceDeployResult, error) {
	args := m.Called(ctx, serviceConfig, serviceContext, targetResource, progress)
	return args.Get(0).(*azdext.ServiceDeployResult), args.Error(1)
}

// Test helper functions
func createTestServiceTargetManager() *ServiceTargetManager {
	return &ServiceTargetManager{
		extensionId:      "microsoft.azd.demo",
		client:           nil, // Not needed for business logic tests
		broker:           nil, // Not needed for business logic tests
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
	manager := NewServiceTargetManager("microsoft.azd.demo", mockClient)

	assert.NotNil(t, manager)
	assert.Equal(t, mockClient, manager.client)
	assert.NotNil(t, manager.componentManager)
	assert.Nil(t, manager.broker) // Broker should be nil until ensureStream is called
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
	req := &ServiceTargetInitializeRequest{
		ServiceConfig: serviceConfig,
	}

	// Execute handler directly
	resp, err := manager.onInitialize(ctx, req)

	// Verify response
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotNil(t, resp.GetInitializeResponse())

	// Verify mock expectations
	mockProvider.AssertExpectations(t)
}

func TestServiceTargetManager_InitializeRequest_NilServiceConfig(t *testing.T) {
	t.Parallel()

	manager := createTestServiceTargetManager()
	ctx := context.Background()

	req := &ServiceTargetInitializeRequest{
		ServiceConfig: nil, // Nil service config
	}

	// Execute handler directly
	resp, err := manager.onInitialize(ctx, req)

	// Verify error response
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service config is required for initialize request")
	assert.Nil(t, resp)
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
	req := &ServiceTargetInitializeRequest{
		ServiceConfig: serviceConfig,
	}

	// Execute handler directly
	resp, err := manager.onInitialize(ctx, req)

	// Verify error response - handler returns the error
	require.Error(t, err)
	assert.Contains(t, err.Error(), "initialization failed")
	// Response should still be created even with error
	require.NotNil(t, resp)
	assert.NotNil(t, resp.GetInitializeResponse())

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
	req := &GetTargetResourceRequest{
		SubscriptionId:        "test-subscription",
		ServiceConfig:  serviceConfig,
		DefaultTargetResource: nil,
		DefaultError:          "",
	}

	// Execute handler directly
	resp, err := manager.onGetTargetResource(ctx, req)

	// Verify response
	require.NoError(t, err)
	require.NotNil(t, resp)
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
	req := &GetTargetResourceRequest{
		SubscriptionId:        "test-subscription",
		ServiceConfig:  serviceConfig,
		DefaultTargetResource: nil,
		DefaultError:          "",
	}

	// Execute handler directly
	resp, err := manager.onGetTargetResource(ctx, req)

	// Verify error response
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no provider instance found for service: web-service")
	assert.Contains(t, err.Error(), "Initialize must be called first")
	assert.Nil(t, resp)
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
	req := &ServiceTargetEndpointsRequest{
		ServiceConfig:  serviceConfig,
		TargetResource: targetResource,
	}

	// Execute handler directly
	resp, err := manager.onEndpoints(ctx, req)

	// Verify response
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotNil(t, resp.GetEndpointsResponse())
	assert.Equal(t, expectedEndpoints, resp.GetEndpointsResponse().Endpoints)

	// Verify mock expectations
	mockProvider.AssertExpectations(t)
}

func TestServiceTargetManager_PackageRequest_NilServiceConfig(t *testing.T) {
	t.Parallel()

	manager := createTestServiceTargetManager()
	ctx := context.Background()

	req := &ServiceTargetPackageRequest{
		ServiceConfig:  nil, // Nil service config
		ServiceContext: &ServiceContext{},
	}

	// Execute handler directly
	resp, err := manager.onPackage(ctx, req, nil)

	// Verify error response
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service config is required for package request")
	assert.Nil(t, resp)
}

func TestServiceTargetManager_PublishRequest_NilServiceConfig(t *testing.T) {
	t.Parallel()

	manager := createTestServiceTargetManager()
	ctx := context.Background()

	req := &ServiceTargetPublishRequest{
		ServiceConfig:  nil, // Nil service config
		ServiceContext: &ServiceContext{},
		TargetResource: &TargetResource{},
		azdext.PublishOptions: &azdext.PublishOptions{},
	}

	// Execute handler directly
	resp, err := manager.onPublish(ctx, req, nil)

	// Verify error response
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service config is required for publish request")
	assert.Nil(t, resp)
}

func TestServiceTargetManager_DeployRequest_NilServiceConfig(t *testing.T) {
	t.Parallel()

	manager := createTestServiceTargetManager()
	ctx := context.Background()

	req := &ServiceTargetDeployRequest{
		ServiceConfig:  nil, // Nil service config
		ServiceContext: &ServiceContext{},
		TargetResource: &TargetResource{},
	}

	// Execute handler directly
	resp, err := manager.onDeploy(ctx, req, nil)

	// Verify error response
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service config is required for deploy request")
	assert.Nil(t, resp)
}

func TestServiceTargetManager_UnknownMessageType(t *testing.T) {
	t.Parallel()

	// This test no longer applies since we removed buildServiceTargetResponseMsg
	// The broker now handles unknown message types and will return errors for unregistered types
	// Testing this would require mocking the broker itself
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
	initReq := &ServiceTargetInitializeRequest{
		ServiceConfig: serviceConfig,
	}
	initResp, err := manager.onInitialize(ctx, initReq)
	require.NoError(t, err)
	require.NotNil(t, initResp)

	// Test endpoints request
	endpointsReq := &ServiceTargetEndpointsRequest{
		ServiceConfig:  serviceConfig,
		TargetResource: targetResource,
	}
	endpointsResp, err := manager.onEndpoints(ctx, endpointsReq)
	require.NoError(t, err)
	require.NotNil(t, endpointsResp)
	assert.Equal(t, expectedEndpoints, endpointsResp.GetEndpointsResponse().Endpoints)

	// Test get target resource request
	getTargetReq := &GetTargetResourceRequest{
		SubscriptionId:        "test-subscription",
		ServiceConfig:  serviceConfig,
		DefaultTargetResource: nil,
		DefaultError:          "",
	}
	getTargetResp, err := manager.onGetTargetResource(ctx, getTargetReq)
	require.NoError(t, err)
	require.NotNil(t, getTargetResp)
	assert.Equal(t, expectedTargetResource, getTargetResp.GetGetTargetResourceResponse().TargetResource)

	// Verify all mock expectations
	mockProvider.AssertExpectations(t)
}
