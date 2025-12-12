// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

// Test_ProjectService_EventDispatcherPreservation validates that EventDispatchers
// are preserved across configuration updates for both projects and services.
// This ensures that event handlers registered by azure.yaml hooks and azd extensions
// continue to work after configuration modifications.
func Test_ProjectService_EventDispatcherPreservation(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	temp := t.TempDir()

	azdContext := azdcontext.NewAzdContextWithDirectory(temp)

	// Step 1: Load project using lazy project config
	projectConfig := &project.ProjectConfig{
		Name: "test-project",
		Services: map[string]*project.ServiceConfig{
			"web": {
				Name:         "web",
				RelativePath: "./src/web",
				Host:         project.ContainerAppTarget,
				Language:     project.ServiceLanguageDotNet,
			},
			"api": {
				Name:         "api",
				RelativePath: "./src/api",
				Host:         project.ContainerAppTarget,
				Language:     project.ServiceLanguagePython,
			},
		},
	}

	// Save initial project configuration
	err := project.Save(*mockContext.Context, projectConfig, azdContext.ProjectPath())
	require.NoError(t, err)

	// Load project config to get proper initialization
	loadedConfig, err := project.Load(*mockContext.Context, azdContext.ProjectPath())
	require.NoError(t, err)

	// Setup lazy dependencies
	lazyAzdContext := lazy.From(azdContext)
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	localDataStore := environment.NewLocalFileDataStore(azdContext, fileConfigManager)
	envManager, err := environment.NewManager(
		mockContext.Container,
		azdContext,
		mockContext.Console,
		localDataStore,
		nil,
	)
	require.NoError(t, err)
	lazyEnvManager := lazy.From(envManager)
	lazyProjectConfig := lazy.From(loadedConfig)

	// Step 2: Register event handlers for project and services
	// EventDispatchers are already initialized by project.Load()
	projectEventCount := atomic.Int32{}
	webServiceEventCount := atomic.Int32{}
	apiServiceEventCount := atomic.Int32{}

	// Register project-level event handler
	err = loadedConfig.AddHandler(
		*mockContext.Context,
		project.ProjectEventDeploy,
		func(ctx context.Context, args project.ProjectLifecycleEventArgs) error {
			projectEventCount.Add(1)
			return nil
		},
	)
	require.NoError(t, err)

	// Register service-level event handlers
	err = loadedConfig.Services["web"].AddHandler(
		*mockContext.Context,
		project.ServiceEventDeploy,
		func(ctx context.Context, args project.ServiceLifecycleEventArgs) error {
			webServiceEventCount.Add(1)
			return nil
		},
	)
	require.NoError(t, err)

	err = loadedConfig.Services["api"].AddHandler(
		*mockContext.Context,
		project.ServiceEventDeploy,
		func(ctx context.Context, args project.ServiceLifecycleEventArgs) error {
			apiServiceEventCount.Add(1)
			return nil
		},
	)
	require.NoError(t, err)

	// Create project service
	service := NewProjectService(lazyAzdContext, lazyEnvManager, lazyProjectConfig)

	// Step 3: Modify project configuration
	customValue, err := structpb.NewValue("project-custom-value")
	require.NoError(t, err)

	_, err = service.SetConfigValue(*mockContext.Context, &azdext.SetProjectConfigValueRequest{
		Path:  "custom.setting",
		Value: customValue,
	})
	require.NoError(t, err)

	// Step 4: Modify service configuration (web)
	webCustomValue, err := structpb.NewValue("web-custom-value")
	require.NoError(t, err)

	_, err = service.SetServiceConfigValue(*mockContext.Context, &azdext.SetServiceConfigValueRequest{
		ServiceName: "web",
		Path:        "custom.endpoint",
		Value:       webCustomValue,
	})
	require.NoError(t, err)

	// Modify service configuration (api)
	apiCustomValue, err := structpb.NewValue("api-custom-value")
	require.NoError(t, err)

	_, err = service.SetServiceConfigValue(*mockContext.Context, &azdext.SetServiceConfigValueRequest{
		ServiceName: "api",
		Path:        "custom.port",
		Value:       apiCustomValue,
	})
	require.NoError(t, err)

	// Step 5: Get the updated project config from lazy loader to verify event dispatchers are preserved
	updatedConfig, err := lazyProjectConfig.GetValue()
	require.NoError(t, err)

	// The project config should be a NEW instance (reloaded from disk)
	require.NotSame(t, loadedConfig, updatedConfig, "project config should be a new instance after reload")

	// But the EventDispatchers should be the SAME instances (preserved pointers)
	require.Same(t, loadedConfig.EventDispatcher, updatedConfig.EventDispatcher,
		"project EventDispatcher should be the same instance (preserved)")
	require.Same(t, loadedConfig.Services["web"].EventDispatcher, updatedConfig.Services["web"].EventDispatcher,
		"web service EventDispatcher should be the same instance (preserved)")
	require.Same(t, loadedConfig.Services["api"].EventDispatcher, updatedConfig.Services["api"].EventDispatcher,
		"api service EventDispatcher should be the same instance (preserved)")

	// Verify event dispatchers are not nil
	require.NotNil(t, updatedConfig.EventDispatcher, "project EventDispatcher should be preserved")
	require.NotNil(
		t,
		updatedConfig.Services["web"].EventDispatcher,
		"web service EventDispatcher should be preserved",
	)
	require.NotNil(
		t,
		updatedConfig.Services["api"].EventDispatcher,
		"api service EventDispatcher should be preserved",
	)

	// Step 6: Invoke event handlers on project by raising the event directly
	err = updatedConfig.RaiseEvent(
		*mockContext.Context,
		project.ProjectEventDeploy,
		project.ProjectLifecycleEventArgs{
			Project: updatedConfig,
		},
	)
	require.NoError(t, err)

	// Step 7: Invoke event handlers on services by raising the events directly
	err = updatedConfig.Services["web"].RaiseEvent(
		*mockContext.Context,
		project.ServiceEventDeploy,
		project.ServiceLifecycleEventArgs{
			Project: updatedConfig,
			Service: updatedConfig.Services["web"],
		},
	)
	require.NoError(t, err)

	err = updatedConfig.Services["api"].RaiseEvent(
		*mockContext.Context,
		project.ServiceEventDeploy,
		project.ServiceLifecycleEventArgs{
			Project: updatedConfig,
			Service: updatedConfig.Services["api"],
		},
	)
	require.NoError(t, err)

	// Step 8: Validate event handlers were invoked
	require.Equal(t, int32(1), projectEventCount.Load(), "project event handler should be invoked once")
	require.Equal(t, int32(1), webServiceEventCount.Load(), "web service event handler should be invoked once")
	require.Equal(t, int32(1), apiServiceEventCount.Load(), "api service event handler should be invoked once")

	// Additional verification: Ensure configuration changes were persisted
	verifyResp, err := service.GetConfigValue(*mockContext.Context, &azdext.GetProjectConfigValueRequest{
		Path: "custom.setting",
	})
	require.NoError(t, err)
	require.True(t, verifyResp.Found)
	require.Equal(t, "project-custom-value", verifyResp.Value.GetStringValue())

	webVerifyResp, err := service.GetServiceConfigValue(*mockContext.Context, &azdext.GetServiceConfigValueRequest{
		ServiceName: "web",
		Path:        "custom.endpoint",
	})
	require.NoError(t, err)
	require.True(t, webVerifyResp.Found)
	require.Equal(t, "web-custom-value", webVerifyResp.Value.GetStringValue())

	apiVerifyResp, err := service.GetServiceConfigValue(*mockContext.Context, &azdext.GetServiceConfigValueRequest{
		ServiceName: "api",
		Path:        "custom.port",
	})
	require.NoError(t, err)
	require.True(t, apiVerifyResp.Found)
	require.Equal(t, "api-custom-value", apiVerifyResp.Value.GetStringValue())
}

// Test_ProjectService_EventDispatcherPreservation_MultipleUpdates tests that event dispatchers
// remain functional after multiple sequential configuration updates.
func Test_ProjectService_EventDispatcherPreservation_MultipleUpdates(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	temp := t.TempDir()

	azdContext := azdcontext.NewAzdContextWithDirectory(temp)

	projectConfig := &project.ProjectConfig{
		Name: "test-project",
		Services: map[string]*project.ServiceConfig{
			"web": {
				Name:         "web",
				RelativePath: "./src/web",
				Host:         project.ContainerAppTarget,
				Language:     project.ServiceLanguageDotNet,
			},
		},
	}

	err := project.Save(*mockContext.Context, projectConfig, azdContext.ProjectPath())
	require.NoError(t, err)

	loadedConfig, err := project.Load(*mockContext.Context, azdContext.ProjectPath())
	require.NoError(t, err)

	lazyAzdContext := lazy.From(azdContext)
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	localDataStore := environment.NewLocalFileDataStore(azdContext, fileConfigManager)
	envManager, err := environment.NewManager(
		mockContext.Container,
		azdContext,
		mockContext.Console,
		localDataStore,
		nil,
	)
	require.NoError(t, err)
	lazyEnvManager := lazy.From(envManager)
	lazyProjectConfig := lazy.From(loadedConfig)

	// Register event handler (EventDispatcher already initialized by project.Load())
	eventCount := atomic.Int32{}
	err = loadedConfig.AddHandler(
		*mockContext.Context,
		project.ProjectEventDeploy,
		func(ctx context.Context, args project.ProjectLifecycleEventArgs) error {
			eventCount.Add(1)
			return nil
		},
	)
	require.NoError(t, err)

	service := NewProjectService(lazyAzdContext, lazyEnvManager, lazyProjectConfig)

	// Perform multiple configuration updates
	for i := 1; i <= 3; i++ {
		value, err := structpb.NewValue(i)
		require.NoError(t, err)

		_, err = service.SetConfigValue(*mockContext.Context, &azdext.SetProjectConfigValueRequest{
			Path:  "custom.counter",
			Value: value,
		})
		require.NoError(t, err)
	}

	// Verify event dispatcher still works after multiple updates
	updatedConfig, err := lazyProjectConfig.GetValue()
	require.NoError(t, err)

	// The project config should be a NEW instance (reloaded from disk)
	require.NotSame(t, loadedConfig, updatedConfig, "project config should be a new instance after reload")

	// But the EventDispatcher should be the SAME instance (preserved pointer)
	require.Same(t, loadedConfig.EventDispatcher, updatedConfig.EventDispatcher,
		"project EventDispatcher should be the same instance (preserved)")
	require.NotNil(t, updatedConfig.EventDispatcher)

	err = updatedConfig.RaiseEvent(
		*mockContext.Context,
		project.ProjectEventDeploy,
		project.ProjectLifecycleEventArgs{Project: updatedConfig},
	)
	require.NoError(t, err)

	require.Equal(t, int32(1), eventCount.Load(), "event handler should be invoked after multiple config updates")
}
