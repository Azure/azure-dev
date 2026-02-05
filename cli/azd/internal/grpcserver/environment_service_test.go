// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
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

// Test_EnvironmentService_ResolveEnvironment validates that methods use the default environment
// when env_name is empty and the specified environment when env_name is provided.
func Test_EnvironmentService_ResolveEnvironment(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	temp := t.TempDir()

	azdContext := azdcontext.NewAzdContextWithDirectory(temp)
	projectConfig := project.ProjectConfig{Name: "test"}
	err := project.Save(*mockContext.Context, &projectConfig, azdContext.ProjectPath())
	require.NoError(t, err)

	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	localDataStore := environment.NewLocalFileDataStore(azdContext, fileConfigManager)
	envManager, err := environment.NewManager(mockContext.Container, azdContext, mockContext.Console, localDataStore, nil)
	require.NoError(t, err)

	lazyAzdContext := lazy.From(azdContext)
	lazyEnvManager := lazy.From(envManager)

	// Create two environments with different values.
	env1, err := envManager.Create(*mockContext.Context, environment.Spec{Name: "env1"})
	require.NoError(t, err)
	env1.DotenvSet("key1", "value1")
	require.NoError(t, envManager.Save(*mockContext.Context, env1))

	env2, err := envManager.Create(*mockContext.Context, environment.Spec{Name: "env2"})
	require.NoError(t, err)
	env2.DotenvSet("key1", "value2")
	require.NoError(t, envManager.Save(*mockContext.Context, env2))

	// Set env1 as default.
	require.NoError(t, azdContext.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "env1"}))

	service := NewEnvironmentService(lazyAzdContext, lazyEnvManager)

	// Test: GetValue with empty env_name falls back to default (env1).
	resp, err := service.GetValue(*mockContext.Context, &azdext.GetEnvRequest{Key: "key1"})
	require.NoError(t, err)
	require.Equal(t, "value1", resp.Value)

	// Test: GetValue with explicit env_name targets the specified environment.
	resp, err = service.GetValue(*mockContext.Context, &azdext.GetEnvRequest{EnvName: "env2", Key: "key1"})
	require.NoError(t, err)
	require.Equal(t, "value2", resp.Value)

	// Test: GetValues with empty name falls back to default (env1).
	valuesResp, err := service.GetValues(*mockContext.Context, &azdext.GetEnvironmentRequest{})
	require.NoError(t, err)
	envValues := map[string]string{}
	for _, kv := range valuesResp.KeyValues {
		envValues[kv.Key] = kv.Value
	}
	require.Equal(t, "value1", envValues["key1"])

	// Test: GetValues with explicit name targets the specified environment.
	valuesResp, err = service.GetValues(*mockContext.Context, &azdext.GetEnvironmentRequest{Name: "env2"})
	require.NoError(t, err)
	envValues = map[string]string{}
	for _, kv := range valuesResp.KeyValues {
		envValues[kv.Key] = kv.Value
	}
	require.Equal(t, "value2", envValues["key1"])

	// Test: SetValue with empty env_name writes to default (env1).
	_, err = service.SetValue(*mockContext.Context, &azdext.SetEnvRequest{Key: "newkey", Value: "newval"})
	require.NoError(t, err)
	resp, err = service.GetValue(*mockContext.Context, &azdext.GetEnvRequest{EnvName: "env1", Key: "newkey"})
	require.NoError(t, err)
	require.Equal(t, "newval", resp.Value)

	// Test: SetConfig with empty env_name writes to default (env1).
	_, err = service.SetConfig(*mockContext.Context, &azdext.SetConfigRequest{
		Path:  "test.key",
		Value: []byte(`"configval1"`),
	})
	require.NoError(t, err)

	// Test: GetConfigString with empty env_name reads from default (env1).
	configResp, err := service.GetConfigString(*mockContext.Context, &azdext.GetConfigStringRequest{Path: "test.key"})
	require.NoError(t, err)
	require.True(t, configResp.Found)
	require.Equal(t, "configval1", configResp.Value)

	// Test: SetConfig with explicit env_name writes to specified environment (env2).
	_, err = service.SetConfig(*mockContext.Context, &azdext.SetConfigRequest{
		Path:    "test.key",
		Value:   []byte(`"configval2"`),
		EnvName: "env2",
	})
	require.NoError(t, err)

	// Test: GetConfigString with explicit env_name reads from specified environment (env2).
	configResp, err = service.GetConfigString(*mockContext.Context, &azdext.GetConfigStringRequest{
		Path:    "test.key",
		EnvName: "env2",
	})
	require.NoError(t, err)
	require.True(t, configResp.Found)
	require.Equal(t, "configval2", configResp.Value)

	// Test: GetConfig with empty env_name reads from default (env1).
	getConfigResp, err := service.GetConfig(*mockContext.Context, &azdext.GetConfigRequest{Path: "test.key"})
	require.NoError(t, err)
	require.True(t, getConfigResp.Found)

	// Test: GetConfig with explicit env_name reads from specified environment (env2).
	getConfigResp, err = service.GetConfig(*mockContext.Context, &azdext.GetConfigRequest{
		Path:    "test.key",
		EnvName: "env2",
	})
	require.NoError(t, err)
	require.True(t, getConfigResp.Found)

	// Test: UnsetConfig with explicit env_name targets specified environment (env2).
	_, err = service.UnsetConfig(*mockContext.Context, &azdext.UnsetConfigRequest{
		Path:    "test.key",
		EnvName: "env2",
	})
	require.NoError(t, err)

	// Verify config was removed from env2 but still exists in env1.
	configResp, err = service.GetConfigString(*mockContext.Context, &azdext.GetConfigStringRequest{
		Path:    "test.key",
		EnvName: "env2",
	})
	require.NoError(t, err)
	require.False(t, configResp.Found)

	configResp, err = service.GetConfigString(*mockContext.Context, &azdext.GetConfigStringRequest{Path: "test.key"})
	require.NoError(t, err)
	require.True(t, configResp.Found)
	require.Equal(t, "configval1", configResp.Value)
}
