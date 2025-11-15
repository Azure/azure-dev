// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/pkg/azdext"
	"github.com/azure/azure-dev/pkg/config"
	"github.com/azure/azure-dev/pkg/environment"
	"github.com/azure/azure-dev/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/pkg/lazy"
	"github.com/azure/azure-dev/pkg/project"
	"github.com/azure/azure-dev/test/mocks"
	"github.com/stretchr/testify/require"
)

// Test_EnvironmentService_NoEnvironment verifies that when no environments are set,
// the GetCurrent method returns an error and List returns an empty list.
func Test_EnvironmentService_NoEnvironment(t *testing.T) {
	// Setup a mock context and temporary project directory.
	mockContext := mocks.NewMockContext(context.Background())
	temp := t.TempDir()

	// Initialize AzdContext.
	azdContext := azdcontext.NewAzdContextWithDirectory(temp)

	// Define and save project configuration.
	projectConfig := project.ProjectConfig{
		Name: "test",
	}
	err := project.Save(*mockContext.Context, &projectConfig, azdContext.ProjectPath())
	require.NoError(t, err)

	// Setup environment data store and manager.
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	localDataStore := environment.NewLocalFileDataStore(azdContext, fileConfigManager)
	envManager, err := environment.NewManager(mockContext.Container, azdContext, mockContext.Console, localDataStore, nil)
	require.NoError(t, err)
	require.NotNil(t, envManager)

	// Initialize lazy loaders.
	lazyAzdContext := lazy.From(azdContext)
	lazyEnvManager := lazy.From(envManager)

	// Create the environment service.
	service := NewEnvironmentService(lazyAzdContext, lazyEnvManager)

	// Test: GetCurrent returns error when there is no default environment.
	_, err = service.GetCurrent(*mockContext.Context, &azdext.EmptyRequest{})
	require.Error(t, err)

	// Test: List returns an empty list when no environments exist.
	listResponse, err := service.List(*mockContext.Context, &azdext.EmptyRequest{})
	require.NoError(t, err)
	require.Equal(t, 0, len(listResponse.Environments))
}

// Test_EnvironmentService_Flow validates the complete flow including:
// environment creation, setting a default and verifying get, list, value retrieval, and selection.
func Test_EnvironmentService_Flow(t *testing.T) {
	// Setup a mock context and temporary project directory.
	mockContext := mocks.NewMockContext(context.Background())
	temp := t.TempDir()

	// Initialize AzdContext.
	azdContext := azdcontext.NewAzdContextWithDirectory(temp)

	// Define and save project configuration.
	projectConfig := project.ProjectConfig{
		Name: "test",
	}
	err := project.Save(*mockContext.Context, &projectConfig, azdContext.ProjectPath())
	require.NoError(t, err)

	// Configure environment data store and manager.
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	localDataStore := environment.NewLocalFileDataStore(azdContext, fileConfigManager)
	envManager, err := environment.NewManager(mockContext.Container, azdContext, mockContext.Console, localDataStore, nil)
	require.NoError(t, err)
	require.NotNil(t, envManager)

	// Create lazy-loaded instances.
	lazyAzdContext := lazy.From(azdContext)
	lazyEnvManager := lazy.From(envManager)

	// Create and validate two test environments.
	testEnv1, err := envManager.Create(*mockContext.Context, environment.Spec{
		Name: "test1",
	})
	require.NoError(t, err)
	require.NotNil(t, testEnv1)

	testEnv2, err := envManager.Create(*mockContext.Context, environment.Spec{
		Name: "test2",
	})
	require.NoError(t, err)

	// Set an environment variable in testEnv1 and save both environments.
	testEnv1.DotenvSet("foo", "bar")
	err = envManager.Save(*mockContext.Context, testEnv1)
	require.NoError(t, err)
	err = envManager.Save(*mockContext.Context, testEnv2)
	require.NoError(t, err)

	// Set default environment.
	err = azdContext.SetProjectState(azdcontext.ProjectState{
		DefaultEnvironment: testEnv1.Name(),
	})
	require.NoError(t, err)

	// Initialize the environment service.
	service := NewEnvironmentService(lazyAzdContext, lazyEnvManager)
	require.NotNil(t, service)

	// Test: GetCurrent returns the default environment.
	getCurrentResponse, err := service.GetCurrent(*mockContext.Context, &azdext.EmptyRequest{})
	require.NoError(t, err)
	require.Equal(t, testEnv1.Name(), getCurrentResponse.Environment.Name)

	// Test: List returns both environments with the correct order.
	listResponse, err := service.List(*mockContext.Context, &azdext.EmptyRequest{})
	require.NoError(t, err)
	require.Equal(t, 2, len(listResponse.Environments))
	require.Equal(t, testEnv1.Name(), listResponse.Environments[0].Name)

	// Test: GetValues returns all key-value pairs from the default environment.
	getValuesResponse, err := service.GetValues(*mockContext.Context, &azdext.GetEnvironmentRequest{
		Name: testEnv1.Name(),
	})
	require.NoError(t, err)
	envValues := map[string]string{}
	for _, kv := range getValuesResponse.KeyValues {
		envValues[kv.Key] = kv.Value
	}
	require.Equal(t, 2, len(getValuesResponse.KeyValues))
	require.Equal(t, testEnv1.Name(), envValues["AZURE_ENV_NAME"])
	require.Equal(t, "bar", envValues["foo"])

	// Test: Select a different environment and verify that GetCurrent updates.
	_, err = service.Select(*mockContext.Context, &azdext.SelectEnvironmentRequest{
		Name: testEnv2.Name(),
	})
	require.NoError(t, err)
	getCurrentResponse, err = service.GetCurrent(*mockContext.Context, &azdext.EmptyRequest{})
	require.NoError(t, err)
	require.Equal(t, testEnv2.Name(), getCurrentResponse.Environment.Name)
}
