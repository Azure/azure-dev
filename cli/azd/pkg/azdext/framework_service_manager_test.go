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

// MockFrameworkServiceProvider implements FrameworkServiceProvider using testify/mock
type MockFrameworkServiceProvider struct {
	mock.Mock
}

func (m *MockFrameworkServiceProvider) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	args := m.Called(ctx, serviceConfig)
	return args.Error(0)
}

func (m *MockFrameworkServiceProvider) RequiredExternalTools(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) ([]*azdext.ExternalTool, error) {
	args := m.Called(ctx, serviceConfig)
	return args.Get(0).([]*azdext.ExternalTool), args.Error(1)
}

func (m *MockFrameworkServiceProvider) Requirements() (*azdext.FrameworkRequirements, error) {
	args := m.Called()
	return args.Get(0).(*azdext.FrameworkRequirements), args.Error(1)
}

func (m *MockFrameworkServiceProvider) Restore(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress ProgressReporter,
) (*azdext.ServiceRestoreResult, error) {
	args := m.Called(ctx, serviceConfig, serviceContext, progress)
	return args.Get(0).(*azdext.ServiceRestoreResult), args.Error(1)
}

func (m *MockFrameworkServiceProvider) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress ProgressReporter,
) (*azdext.ServiceBuildResult, error) {
	args := m.Called(ctx, serviceConfig, serviceContext, progress)
	return args.Get(0).(*azdext.ServiceBuildResult), args.Error(1)
}

func (m *MockFrameworkServiceProvider) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress ProgressReporter,
) (*azdext.ServicePackageResult, error) {
	args := m.Called(ctx, serviceConfig, serviceContext, progress)
	return args.Get(0).(*azdext.ServicePackageResult), args.Error(1)
}

// Test helper functions
func createTestFrameworkServiceManager() *FrameworkServiceManager {
	return &FrameworkServiceManager{
		extensionId:      "microsoft.azd.demo",
		client:           nil, // Not needed for business logic tests
		broker:           nil, // Not needed for business logic tests
		componentManager: NewComponentManager[FrameworkServiceProvider](FrameworkServiceFactoryKey, "framework service"),
	}
}

func createTestServiceConfigForFramework(name, language string) *ServiceConfig {
	return &ServiceConfig{
		Name:     name,
		Language: language,
	}
}

func TestNewFrameworkServiceManager(t *testing.T) {
	t.Parallel()

	mockClient := &AzdClient{} // Can be nil for this test
	manager := NewFrameworkServiceManager("microsoft.azd.demo", mockClient)

	assert.NotNil(t, manager)
	assert.Equal(t, mockClient, manager.client)
	assert.NotNil(t, manager.componentManager)
	assert.Nil(t, manager.broker) // Broker should be nil until ensureStream is called
}

func TestFrameworkServiceManager_FactoryRegistration(t *testing.T) {
	t.Parallel()

	manager := createTestFrameworkServiceManager()

	// Create a mock factory
	factory := func() FrameworkServiceProvider {
		provider := &MockFrameworkServiceProvider{}
		provider.On("Initialize", mock.Anything, mock.Anything).Return(nil)
		return provider
	}

	// Register factory through component manager
	manager.componentManager.RegisterFactory("go", factory)

	// Verify factory is registered
	assert.True(t, manager.componentManager.HasFactory("go"))
	assert.False(t, manager.componentManager.HasFactory("python"))
}

func TestFrameworkServiceManager_InitializeRequest_Success(t *testing.T) {
	t.Parallel()

	manager := createTestFrameworkServiceManager()
	ctx := context.Background()

	// Set up mock provider
	mockProvider := &MockFrameworkServiceProvider{}
	mockProvider.On("Initialize", mock.Anything, mock.Anything).Return(nil)

	factory := func() FrameworkServiceProvider {
		return mockProvider
	}
	manager.componentManager.RegisterFactory("go", factory)

	// Create initialize request
	serviceConfig := createTestServiceConfigForFramework("web-service", "go")
	req := &FrameworkServiceInitializeRequest{
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

func TestFrameworkServiceManager_InitializeRequest_NilServiceConfig(t *testing.T) {
	t.Parallel()

	manager := createTestFrameworkServiceManager()
	ctx := context.Background()

	req := &FrameworkServiceInitializeRequest{
		ServiceConfig: nil, // Nil service config
	}

	// Execute handler directly
	resp, err := manager.onInitialize(ctx, req)

	// Verify error response
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service config is required for initialize request")
	assert.Nil(t, resp)
}

func TestFrameworkServiceManager_InitializeRequest_ProviderInitializationError(t *testing.T) {
	t.Parallel()

	manager := createTestFrameworkServiceManager()
	ctx := context.Background()

	// Set up mock provider with initialization error
	expectedError := errors.New("initialization failed")
	mockProvider := &MockFrameworkServiceProvider{}
	mockProvider.On("Initialize", mock.Anything, mock.Anything).Return(expectedError)

	factory := func() FrameworkServiceProvider {
		return mockProvider
	}
	manager.componentManager.RegisterFactory("go", factory)

	// Create initialize request
	serviceConfig := createTestServiceConfigForFramework("web-service", "go")
	req := &FrameworkServiceInitializeRequest{
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

func TestFrameworkServiceManager_RequiredExternalToolsRequest_Success(t *testing.T) {
	t.Parallel()

	manager := createTestFrameworkServiceManager()
	ctx := context.Background()

	// Set up mock provider
	mockProvider := &MockFrameworkServiceProvider{}
	mockProvider.On("Initialize", mock.Anything, mock.Anything).Return(nil)

	expectedTools := []*azdext.ExternalTool{
		{Name: "go", InstallUrl: "https://go.dev/dl/"},
		{Name: "docker", InstallUrl: "https://docker.com/get-started"},
	}
	mockProvider.On("RequiredExternalTools", mock.Anything, mock.Anything).Return(expectedTools, nil)

	factory := func() FrameworkServiceProvider {
		return mockProvider
	}
	manager.componentManager.RegisterFactory("go", factory)

	// Create and initialize instance first
	serviceConfig := createTestServiceConfigForFramework("web-service", "go")
	_, err := manager.componentManager.GetOrCreateInstance(ctx, serviceConfig)
	require.NoError(t, err)

	// Create required external tools request
	req := &FrameworkServiceRequiredExternalToolsRequest{
		ServiceConfig: serviceConfig,
	}

	// Execute handler directly
	resp, err := manager.onRequiredExternalTools(ctx, req)

	// Verify response
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotNil(t, resp.GetRequiredExternalToolsResponse())
	assert.Equal(t, expectedTools, resp.GetRequiredExternalToolsResponse().Tools)

	// Verify mock expectations
	mockProvider.AssertExpectations(t)
}

func TestFrameworkServiceManager_RequiredExternalToolsRequest_NoProvider(t *testing.T) {
	t.Parallel()

	manager := createTestFrameworkServiceManager()
	ctx := context.Background()

	// Create request without initializing any provider
	serviceConfig := createTestServiceConfigForFramework("web-service", "go")
	req := &FrameworkServiceRequiredExternalToolsRequest{
		ServiceConfig: serviceConfig,
	}

	// Execute handler directly
	resp, err := manager.onRequiredExternalTools(ctx, req)

	// Verify error response
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no provider instance found")
	assert.Nil(t, resp)
}

func TestFrameworkServiceManager_RequirementsRequest_Success(t *testing.T) {
	t.Parallel()

	manager := createTestFrameworkServiceManager()
	ctx := context.Background()

	// Set up mock provider
	mockProvider := &MockFrameworkServiceProvider{}
	mockProvider.On("Initialize", mock.Anything, mock.Anything).Return(nil)

	expectedReq := &azdext.FrameworkRequirements{
		Package: &FrameworkPackageRequirements{
			RequireRestore: true,
			RequireBuild:   true,
		},
	}
	mockProvider.On("Requirements").Return(expectedReq, nil)

	factory := func() FrameworkServiceProvider {
		return mockProvider
	}
	manager.componentManager.RegisterFactory("go", factory)

	// Create and initialize instance first
	serviceConfig := createTestServiceConfigForFramework("web-service", "go")
	_, err := manager.componentManager.GetOrCreateInstance(ctx, serviceConfig)
	require.NoError(t, err)

	// Create requirements request (it has no fields)
	req := &FrameworkServiceRequirementsRequest{}

	// Execute handler directly
	resp, err := manager.onRequirements(ctx, req)

	// Verify response
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotNil(t, resp.GetRequirementsResponse())
	assert.Equal(t, expectedReq, resp.GetRequirementsResponse().Requirements)

	// Verify mock expectations
	mockProvider.AssertExpectations(t)
}

func TestFrameworkServiceManager_RequirementsRequest_NoProvider(t *testing.T) {
	t.Parallel()

	manager := createTestFrameworkServiceManager()
	ctx := context.Background()

	// Create requirements request (it has no fields)
	req := &FrameworkServiceRequirementsRequest{}

	// Execute handler directly
	resp, err := manager.onRequirements(ctx, req)

	// Verify error response
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no provider instances available")
	assert.Nil(t, resp)
}

func TestFrameworkServiceManager_RestoreRequest_NilServiceConfig(t *testing.T) {
	t.Parallel()

	manager := createTestFrameworkServiceManager()
	ctx := context.Background()

	req := &FrameworkServiceRestoreRequest{
		ServiceConfig:  nil, // Nil service config
		ServiceContext: &ServiceContext{},
	}

	// Execute handler directly (progress func can be nil for this validation test)
	resp, err := manager.onRestore(ctx, req, nil)

	// Verify error response
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service config is required for restore request")
	assert.Nil(t, resp)
}

func TestFrameworkServiceManager_BuildRequest_NilServiceConfig(t *testing.T) {
	t.Parallel()

	manager := createTestFrameworkServiceManager()
	ctx := context.Background()

	req := &FrameworkServiceBuildRequest{
		ServiceConfig:  nil, // Nil service config
		ServiceContext: &ServiceContext{},
	}

	// Execute handler directly (progress func can be nil for this validation test)
	resp, err := manager.onBuild(ctx, req, nil)

	// Verify error response
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service config is required for build request")
	assert.Nil(t, resp)
}

func TestFrameworkServiceManager_PackageRequest_NilServiceConfig(t *testing.T) {
	t.Parallel()

	manager := createTestFrameworkServiceManager()
	ctx := context.Background()

	req := &FrameworkServicePackageRequest{
		ServiceConfig:  nil, // Nil service config
		ServiceContext: &ServiceContext{},
	}

	// Execute handler directly (progress func can be nil for this validation test)
	resp, err := manager.onPackage(ctx, req, nil)

	// Verify error response
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service config is required for package request")
	assert.Nil(t, resp)
}

func TestFrameworkServiceManager_Close_ComponentManagerIntegration(t *testing.T) {
	t.Parallel()

	manager := createTestFrameworkServiceManager()

	// Register a factory and create instance
	mockProvider := &MockFrameworkServiceProvider{}
	mockProvider.On("Initialize", mock.Anything, mock.Anything).Return(nil)

	factory := func() FrameworkServiceProvider {
		return mockProvider
	}
	manager.componentManager.RegisterFactory("go", factory)

	ctx := context.Background()
	serviceConfig := createTestServiceConfigForFramework("web-service", "go")
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
