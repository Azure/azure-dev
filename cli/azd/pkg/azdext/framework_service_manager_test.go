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
) ([]*ExternalTool, error) {
	args := m.Called(ctx, serviceConfig)
	return args.Get(0).([]*ExternalTool), args.Error(1)
}

func (m *MockFrameworkServiceProvider) Requirements() (*FrameworkRequirements, error) {
	args := m.Called()
	return args.Get(0).(*FrameworkRequirements), args.Error(1)
}

func (m *MockFrameworkServiceProvider) Restore(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress ProgressReporter,
) (*ServiceRestoreResult, error) {
	args := m.Called(ctx, serviceConfig, serviceContext, progress)
	return args.Get(0).(*ServiceRestoreResult), args.Error(1)
}

func (m *MockFrameworkServiceProvider) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress ProgressReporter,
) (*ServiceBuildResult, error) {
	args := m.Called(ctx, serviceConfig, serviceContext, progress)
	return args.Get(0).(*ServiceBuildResult), args.Error(1)
}

func (m *MockFrameworkServiceProvider) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress ProgressReporter,
) (*ServicePackageResult, error) {
	args := m.Called(ctx, serviceConfig, serviceContext, progress)
	return args.Get(0).(*ServicePackageResult), args.Error(1)
}

// Test helper functions
func createTestFrameworkServiceManager() *FrameworkServiceManager {
	return &FrameworkServiceManager{
		client:           nil, // Not needed for business logic tests
		stream:           nil, // Not needed for business logic tests
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
	manager := NewFrameworkServiceManager(mockClient)

	assert.NotNil(t, manager)
	assert.Equal(t, mockClient, manager.client)
	assert.NotNil(t, manager.componentManager)
	assert.Nil(t, manager.stream) // Stream should be nil until Register is called
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
	requestId := "init-123"
	msg := &FrameworkServiceMessage{
		RequestId: requestId,
		MessageType: &FrameworkServiceMessage_InitializeRequest{
			InitializeRequest: &FrameworkServiceInitializeRequest{
				ServiceConfig: serviceConfig,
			},
		},
	}

	// Execute
	resp := manager.buildFrameworkServiceResponseMsg(ctx, msg)

	// Verify response
	require.NotNil(t, resp)
	assert.Equal(t, requestId, resp.RequestId)
	assert.Nil(t, resp.Error)
	assert.NotNil(t, resp.GetInitializeResponse())

	// Verify mock expectations
	mockProvider.AssertExpectations(t)
}

func TestFrameworkServiceManager_InitializeRequest_NilServiceConfig(t *testing.T) {
	t.Parallel()

	manager := createTestFrameworkServiceManager()
	ctx := context.Background()

	requestId := "init-123"
	msg := &FrameworkServiceMessage{
		RequestId: requestId,
		MessageType: &FrameworkServiceMessage_InitializeRequest{
			InitializeRequest: &FrameworkServiceInitializeRequest{
				ServiceConfig: nil, // Nil service config
			},
		},
	}

	// Execute
	resp := manager.buildFrameworkServiceResponseMsg(ctx, msg)

	// Verify error response
	require.NotNil(t, resp)
	assert.Equal(t, requestId, resp.RequestId)
	assert.NotNil(t, resp.Error)
	assert.Contains(t, resp.Error.Message, "service config is required for initialize request")
	assert.Nil(t, resp.GetInitializeResponse())
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
	requestId := "init-123"
	msg := &FrameworkServiceMessage{
		RequestId: requestId,
		MessageType: &FrameworkServiceMessage_InitializeRequest{
			InitializeRequest: &FrameworkServiceInitializeRequest{
				ServiceConfig: serviceConfig,
			},
		},
	}

	// Execute
	resp := manager.buildFrameworkServiceResponseMsg(ctx, msg)

	// Verify error response
	require.NotNil(t, resp)
	assert.Equal(t, requestId, resp.RequestId)
	assert.NotNil(t, resp.Error)
	assert.Contains(t, resp.Error.Message, "initialization failed")
	assert.NotNil(t, resp.GetInitializeResponse()) // Response should still be created

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

	expectedTools := []*ExternalTool{
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
	requestId := "tools-123"
	msg := &FrameworkServiceMessage{
		RequestId: requestId,
		MessageType: &FrameworkServiceMessage_RequiredExternalToolsRequest{
			RequiredExternalToolsRequest: &FrameworkServiceRequiredExternalToolsRequest{
				ServiceConfig: serviceConfig,
			},
		},
	}

	// Execute
	resp := manager.buildFrameworkServiceResponseMsg(ctx, msg)

	// Verify response
	require.NotNil(t, resp)
	assert.Equal(t, requestId, resp.RequestId)
	assert.Nil(t, resp.Error)
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
	requestId := "tools-123"
	msg := &FrameworkServiceMessage{
		RequestId: requestId,
		MessageType: &FrameworkServiceMessage_RequiredExternalToolsRequest{
			RequiredExternalToolsRequest: &FrameworkServiceRequiredExternalToolsRequest{
				ServiceConfig: serviceConfig,
			},
		},
	}

	// Execute
	resp := manager.buildFrameworkServiceResponseMsg(ctx, msg)

	// Verify error response
	require.NotNil(t, resp)
	assert.Equal(t, requestId, resp.RequestId)
	assert.NotNil(t, resp.Error)
	assert.Contains(t, resp.Error.Message, "no provider instance found for service: web-service")
	assert.Contains(t, resp.Error.Message, "Initialize must be called first")
	assert.Nil(t, resp.GetRequiredExternalToolsResponse())
}

func TestFrameworkServiceManager_RequirementsRequest_Success(t *testing.T) {
	t.Parallel()

	manager := createTestFrameworkServiceManager()
	ctx := context.Background()

	// Set up mock provider
	mockProvider := &MockFrameworkServiceProvider{}
	mockProvider.On("Initialize", mock.Anything, mock.Anything).Return(nil)

	expectedRequirements := &FrameworkRequirements{
		Package: &FrameworkPackageRequirements{},
	}
	mockProvider.On("Requirements").Return(expectedRequirements, nil)

	factory := func() FrameworkServiceProvider {
		return mockProvider
	}
	manager.componentManager.RegisterFactory("go", factory)

	// Create and initialize instance first
	serviceConfig := createTestServiceConfigForFramework("web-service", "go")
	_, err := manager.componentManager.GetOrCreateInstance(ctx, serviceConfig)
	require.NoError(t, err)

	// Create requirements request (doesn't need specific service config)
	requestId := "req-123"
	msg := &FrameworkServiceMessage{
		RequestId: requestId,
		MessageType: &FrameworkServiceMessage_RequirementsRequest{
			RequirementsRequest: &FrameworkServiceRequirementsRequest{},
		},
	}

	// Execute
	resp := manager.buildFrameworkServiceResponseMsg(ctx, msg)

	// Verify response
	require.NotNil(t, resp)
	assert.Equal(t, requestId, resp.RequestId)
	assert.Nil(t, resp.Error)
	assert.NotNil(t, resp.GetRequirementsResponse())
	assert.Equal(t, expectedRequirements, resp.GetRequirementsResponse().Requirements)

	// Verify mock expectations
	mockProvider.AssertExpectations(t)
}

func TestFrameworkServiceManager_RequirementsRequest_NoProvider(t *testing.T) {
	t.Parallel()

	manager := createTestFrameworkServiceManager()
	ctx := context.Background()

	// Create requirements request without any initialized providers
	requestId := "req-123"
	msg := &FrameworkServiceMessage{
		RequestId: requestId,
		MessageType: &FrameworkServiceMessage_RequirementsRequest{
			RequirementsRequest: &FrameworkServiceRequirementsRequest{},
		},
	}

	// Execute
	resp := manager.buildFrameworkServiceResponseMsg(ctx, msg)

	// Verify error response
	require.NotNil(t, resp)
	assert.Equal(t, requestId, resp.RequestId)
	assert.NotNil(t, resp.Error)
	assert.Contains(t, resp.Error.Message, "no provider instances available")
	assert.Contains(t, resp.Error.Message, "Initialize must be called first")
	assert.Nil(t, resp.GetRequirementsResponse())
}

func TestFrameworkServiceManager_RestoreRequest_NilServiceConfig(t *testing.T) {
	t.Parallel()

	manager := createTestFrameworkServiceManager()
	ctx := context.Background()

	requestId := "restore-123"
	msg := &FrameworkServiceMessage{
		RequestId: requestId,
		MessageType: &FrameworkServiceMessage_RestoreRequest{
			RestoreRequest: &FrameworkServiceRestoreRequest{
				ServiceConfig:  nil, // Nil service config
				ServiceContext: &ServiceContext{},
			},
		},
	}

	// Execute
	resp := manager.buildFrameworkServiceResponseMsg(ctx, msg)

	// Verify error response
	require.NotNil(t, resp)
	assert.Equal(t, requestId, resp.RequestId)
	assert.NotNil(t, resp.Error)
	assert.Contains(t, resp.Error.Message, "service config is required for restore request")
	assert.Nil(t, resp.GetRestoreResponse())
}

func TestFrameworkServiceManager_BuildRequest_NilServiceConfig(t *testing.T) {
	t.Parallel()

	manager := createTestFrameworkServiceManager()
	ctx := context.Background()

	requestId := "build-123"
	msg := &FrameworkServiceMessage{
		RequestId: requestId,
		MessageType: &FrameworkServiceMessage_BuildRequest{
			BuildRequest: &FrameworkServiceBuildRequest{
				ServiceConfig:  nil, // Nil service config
				ServiceContext: &ServiceContext{},
			},
		},
	}

	// Execute
	resp := manager.buildFrameworkServiceResponseMsg(ctx, msg)

	// Verify error response
	require.NotNil(t, resp)
	assert.Equal(t, requestId, resp.RequestId)
	assert.NotNil(t, resp.Error)
	assert.Contains(t, resp.Error.Message, "service config is required for build request")
	assert.Nil(t, resp.GetBuildResponse())
}

func TestFrameworkServiceManager_PackageRequest_NilServiceConfig(t *testing.T) {
	t.Parallel()

	manager := createTestFrameworkServiceManager()
	ctx := context.Background()

	requestId := "package-123"
	msg := &FrameworkServiceMessage{
		RequestId: requestId,
		MessageType: &FrameworkServiceMessage_PackageRequest{
			PackageRequest: &FrameworkServicePackageRequest{
				ServiceConfig:  nil, // Nil service config
				ServiceContext: &ServiceContext{},
			},
		},
	}

	// Execute
	resp := manager.buildFrameworkServiceResponseMsg(ctx, msg)

	// Verify error response
	require.NotNil(t, resp)
	assert.Equal(t, requestId, resp.RequestId)
	assert.NotNil(t, resp.Error)
	assert.Contains(t, resp.Error.Message, "service config is required for package request")
	assert.Nil(t, resp.GetPackageResponse())
}

func TestFrameworkServiceManager_UnsupportedMessageType(t *testing.T) {
	t.Parallel()

	manager := createTestFrameworkServiceManager()
	ctx := context.Background()

	// Create message with unsupported type
	requestId := "unknown-123"
	msg := &FrameworkServiceMessage{
		RequestId:   requestId,
		MessageType: nil, // Nil message type (unsupported)
	}

	// Execute
	resp := manager.buildFrameworkServiceResponseMsg(ctx, msg)

	// Verify error response
	require.NotNil(t, resp)
	assert.Equal(t, requestId, resp.RequestId)
	assert.NotNil(t, resp.Error)
	assert.Contains(t, resp.Error.Message, "unsupported message type")
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

func TestFrameworkServiceManager_RestoreRequest_Success(t *testing.T) {
	t.Parallel()

	manager := createTestFrameworkServiceManager()
	ctx := context.Background()

	// Set up mock provider
	mockProvider := &MockFrameworkServiceProvider{}
	mockProvider.On("Initialize", mock.Anything, mock.Anything).Return(nil)

	expectedResult := &ServiceRestoreResult{
		Artifacts: []*Artifact{
			{
				Kind:         ArtifactKind_ARTIFACT_KIND_DIRECTORY,
				Location:     "./src",
				LocationKind: LocationKind_LOCATION_KIND_LOCAL,
			},
		},
	}
	mockProvider.On("Restore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(expectedResult, nil)

	factory := func() FrameworkServiceProvider {
		return mockProvider
	}
	manager.componentManager.RegisterFactory("go", factory)

	// Create and initialize instance first
	serviceConfig := createTestServiceConfigForFramework("web-service", "go")
	_, err := manager.componentManager.GetOrCreateInstance(ctx, serviceConfig)
	require.NoError(t, err)

	// Create restore request
	requestId := "restore-123"
	serviceContext := &ServiceContext{}
	msg := &FrameworkServiceMessage{
		RequestId: requestId,
		MessageType: &FrameworkServiceMessage_RestoreRequest{
			RestoreRequest: &FrameworkServiceRestoreRequest{
				ServiceConfig:  serviceConfig,
				ServiceContext: serviceContext,
			},
		},
	}

	// Execute
	resp := manager.buildFrameworkServiceResponseMsg(ctx, msg)

	// Verify response
	require.NotNil(t, resp)
	assert.Equal(t, requestId, resp.RequestId)
	assert.Nil(t, resp.Error)
	assert.NotNil(t, resp.GetRestoreResponse())
	assert.Equal(t, expectedResult, resp.GetRestoreResponse().RestoreResult)

	// Verify mock expectations - note that the progress reporter function is passed as an argument
	mockProvider.AssertExpectations(t)
}

func TestFrameworkServiceManager_BuildRequest_Success(t *testing.T) {
	t.Parallel()

	manager := createTestFrameworkServiceManager()
	ctx := context.Background()

	// Set up mock provider
	mockProvider := &MockFrameworkServiceProvider{}
	mockProvider.On("Initialize", mock.Anything, mock.Anything).Return(nil)

	expectedResult := &ServiceBuildResult{
		Artifacts: []*Artifact{
			{
				Kind:         ArtifactKind_ARTIFACT_KIND_ARCHIVE,
				Location:     "./dist/app.zip",
				LocationKind: LocationKind_LOCATION_KIND_LOCAL,
			},
		},
	}
	mockProvider.On("Build", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(expectedResult, nil)

	factory := func() FrameworkServiceProvider {
		return mockProvider
	}
	manager.componentManager.RegisterFactory("go", factory)

	// Create and initialize instance first
	serviceConfig := createTestServiceConfigForFramework("web-service", "go")
	_, err := manager.componentManager.GetOrCreateInstance(ctx, serviceConfig)
	require.NoError(t, err)

	// Create build request
	requestId := "build-123"
	serviceContext := &ServiceContext{}
	msg := &FrameworkServiceMessage{
		RequestId: requestId,
		MessageType: &FrameworkServiceMessage_BuildRequest{
			BuildRequest: &FrameworkServiceBuildRequest{
				ServiceConfig:  serviceConfig,
				ServiceContext: serviceContext,
			},
		},
	}

	// Execute
	resp := manager.buildFrameworkServiceResponseMsg(ctx, msg)

	// Verify response
	require.NotNil(t, resp)
	assert.Equal(t, requestId, resp.RequestId)
	assert.Nil(t, resp.Error)
	assert.NotNil(t, resp.GetBuildResponse())
	assert.Equal(t, expectedResult, resp.GetBuildResponse().Result)

	// Verify mock expectations
	mockProvider.AssertExpectations(t)
}

func TestFrameworkServiceManager_PackageRequest_Success(t *testing.T) {
	t.Parallel()

	manager := createTestFrameworkServiceManager()
	ctx := context.Background()

	// Set up mock provider
	mockProvider := &MockFrameworkServiceProvider{}
	mockProvider.On("Initialize", mock.Anything, mock.Anything).Return(nil)

	expectedResult := &ServicePackageResult{
		Artifacts: []*Artifact{
			{
				Kind:         ArtifactKind_ARTIFACT_KIND_ARCHIVE,
				Location:     "./dist/app.zip",
				LocationKind: LocationKind_LOCATION_KIND_LOCAL,
			},
		},
	}
	mockProvider.On("Package", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(expectedResult, nil)

	factory := func() FrameworkServiceProvider {
		return mockProvider
	}
	manager.componentManager.RegisterFactory("go", factory)

	// Create and initialize instance first
	serviceConfig := createTestServiceConfigForFramework("web-service", "go")
	_, err := manager.componentManager.GetOrCreateInstance(ctx, serviceConfig)
	require.NoError(t, err)

	// Create package request
	requestId := "package-123"
	serviceContext := &ServiceContext{}
	msg := &FrameworkServiceMessage{
		RequestId: requestId,
		MessageType: &FrameworkServiceMessage_PackageRequest{
			PackageRequest: &FrameworkServicePackageRequest{
				ServiceConfig:  serviceConfig,
				ServiceContext: serviceContext,
			},
		},
	}

	// Execute
	resp := manager.buildFrameworkServiceResponseMsg(ctx, msg)

	// Verify response
	require.NotNil(t, resp)
	assert.Equal(t, requestId, resp.RequestId)
	assert.Nil(t, resp.Error)
	assert.NotNil(t, resp.GetPackageResponse())
	assert.Equal(t, expectedResult, resp.GetPackageResponse().PackageResult)

	// Verify mock expectations
	mockProvider.AssertExpectations(t)
}

func TestFrameworkServiceManager_MultipleRequestTypes_SameProvider(t *testing.T) {
	t.Parallel()

	manager := createTestFrameworkServiceManager()
	ctx := context.Background()

	// Set up mock provider with multiple method expectations
	mockProvider := &MockFrameworkServiceProvider{}
	mockProvider.On("Initialize", mock.Anything, mock.Anything).Return(nil)

	expectedTools := []*ExternalTool{
		{Name: "go", InstallUrl: "https://go.dev/dl/"},
	}
	mockProvider.On("RequiredExternalTools", mock.Anything, mock.Anything).Return(expectedTools, nil)

	expectedRequirements := &FrameworkRequirements{
		Package: &FrameworkPackageRequirements{
			RequireRestore: true,
			RequireBuild:   true,
		},
	}
	mockProvider.On("Requirements").Return(expectedRequirements, nil)

	factory := func() FrameworkServiceProvider {
		return mockProvider
	}
	manager.componentManager.RegisterFactory("go", factory)

	serviceConfig := createTestServiceConfigForFramework("web-service", "go")

	// Initialize first
	initMsg := &FrameworkServiceMessage{
		RequestId: "init-123",
		MessageType: &FrameworkServiceMessage_InitializeRequest{
			InitializeRequest: &FrameworkServiceInitializeRequest{
				ServiceConfig: serviceConfig,
			},
		},
	}
	initResp := manager.buildFrameworkServiceResponseMsg(ctx, initMsg)
	require.NotNil(t, initResp)
	assert.Nil(t, initResp.Error)

	// Test external tools request
	toolsMsg := &FrameworkServiceMessage{
		RequestId: "tools-123",
		MessageType: &FrameworkServiceMessage_RequiredExternalToolsRequest{
			RequiredExternalToolsRequest: &FrameworkServiceRequiredExternalToolsRequest{
				ServiceConfig: serviceConfig,
			},
		},
	}
	toolsResp := manager.buildFrameworkServiceResponseMsg(ctx, toolsMsg)
	require.NotNil(t, toolsResp)
	assert.Nil(t, toolsResp.Error)
	assert.Equal(t, expectedTools, toolsResp.GetRequiredExternalToolsResponse().Tools)

	// Test requirements request
	reqMsg := &FrameworkServiceMessage{
		RequestId: "req-123",
		MessageType: &FrameworkServiceMessage_RequirementsRequest{
			RequirementsRequest: &FrameworkServiceRequirementsRequest{},
		},
	}
	reqResp := manager.buildFrameworkServiceResponseMsg(ctx, reqMsg)
	require.NotNil(t, reqResp)
	assert.Nil(t, reqResp.Error)
	assert.Equal(t, expectedRequirements, reqResp.GetRequirementsResponse().Requirements)

	// Verify all mock expectations
	mockProvider.AssertExpectations(t)
}
