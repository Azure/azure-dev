// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/state"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

// setupTestEnvironment creates a test environment with config data
func setupTestEnvironment(t *testing.T, envName string, configData map[string]any) (
	*azdcontext.AzdContext,
	environment.Manager,
	string,
) {
	tempDir := t.TempDir()
	envDir := filepath.Join(tempDir, "project")
	require.NoError(t, os.MkdirAll(filepath.Join(envDir, ".azure", envName), 0755))

	azdCtx := azdcontext.NewAzdContextWithDirectory(envDir)
	env := environment.New(envName)
	env.Config = config.NewConfig(configData)

	// Create config manager
	configManager := config.NewFileConfigManager(config.NewManager())
	localDataStore := environment.NewLocalFileDataStore(azdCtx, configManager)

	// Save environment
	err := localDataStore.Save(context.Background(), env, &environment.SaveOptions{IsNew: true})
	require.NoError(t, err)

	// Create mock context and register environment manager
	mockContext := mocks.NewMockContext(context.Background())
	mockContext.Container.MustRegisterSingleton(func() *azdcontext.AzdContext {
		return azdCtx
	})
	mockContext.Container.MustRegisterSingleton(func() environment.LocalDataStore {
		return localDataStore
	})
	mockContext.Container.MustRegisterSingleton(func() *state.RemoteConfig {
		return nil
	})
	mockContext.Container.MustRegisterSingleton(environment.NewManager)

	// Get environment manager from container
	var envManager environment.Manager
	err = mockContext.Container.Resolve(&envManager)
	require.NoError(t, err)

	return azdCtx, envManager, envDir
}

// TestEnvConfigGet tests the azd env config get command
func TestEnvConfigGet(t *testing.T) {
	tests := []struct {
		name          string
		configData    map[string]any
		path          string
		expectedValue any
		expectError   bool
		errorContains string
	}{
		{
			name: "GetSimpleValue",
			configData: map[string]any{
				"key": "value",
			},
			path:          "key",
			expectedValue: "value",
			expectError:   false,
		},
		{
			name: "GetNestedValue",
			configData: map[string]any{
				"app": map[string]any{
					"endpoint": "https://example.com",
				},
			},
			path:          "app.endpoint",
			expectedValue: "https://example.com",
			expectError:   false,
		},
		{
			name: "GetNestedObject",
			configData: map[string]any{
				"app": map[string]any{
					"endpoint": "https://example.com",
					"port":     "8080",
				},
			},
			path: "app",
			expectedValue: map[string]any{
				"endpoint": "https://example.com",
				"port":     "8080",
			},
			expectError: false,
		},
		{
			name: "GetNonExistentKey",
			configData: map[string]any{
				"key": "value",
			},
			path:          "nonexistent",
			expectError:   true,
			errorContains: "no value stored at path",
		},
		{
			name: "GetDeeplyNestedValue",
			configData: map[string]any{
				"level1": map[string]any{
					"level2": map[string]any{
						"level3": "deep-value",
					},
				},
			},
			path:          "level1.level2.level3",
			expectedValue: "deep-value",
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envName := "test-env"
			azdCtx, envManager, _ := setupTestEnvironment(t, envName, tt.configData)

			// Setup action
			buf := &bytes.Buffer{}
			flags := &envConfigGetFlags{}
			flags.EnvironmentName = envName
			action := newEnvConfigGetAction(
				azdCtx,
				envManager,
				&output.JsonFormatter{},
				buf,
				flags,
				[]string{tt.path},
			)

			// Run action
			_, err := action.Run(context.Background())

			// Verify results
			if tt.expectError {
				require.Error(t, err)
				if tt.errorContains != "" {
					require.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				require.NoError(t, err)

				var result any
				err = json.Unmarshal(buf.Bytes(), &result)
				require.NoError(t, err)
				require.Equal(t, tt.expectedValue, result)
			}
		})
	}
}

// TestEnvConfigSet tests the azd env config set command
func TestEnvConfigSet(t *testing.T) {
	tests := []struct {
		name           string
		initialConfig  map[string]any
		path           string
		value          string
		expectedConfig map[string]any
		expectError    bool
	}{
		{
			name:          "SetSimpleValue",
			initialConfig: map[string]any{},
			path:          "key",
			value:         "value",
			expectedConfig: map[string]any{
				"key": "value",
			},
			expectError: false,
		},
		{
			name:          "SetNestedValue",
			initialConfig: map[string]any{},
			path:          "app.endpoint",
			value:         "https://example.com",
			expectedConfig: map[string]any{
				"app": map[string]any{
					"endpoint": "https://example.com",
				},
			},
			expectError: false,
		},
		{
			name: "UpdateExistingValue",
			initialConfig: map[string]any{
				"key": "old-value",
			},
			path:  "key",
			value: "new-value",
			expectedConfig: map[string]any{
				"key": "new-value",
			},
			expectError: false,
		},
		{
			name: "AddToExistingObject",
			initialConfig: map[string]any{
				"app": map[string]any{
					"endpoint": "https://example.com",
				},
			},
			path:  "app.port",
			value: "8080",
			expectedConfig: map[string]any{
				"app": map[string]any{
					"endpoint": "https://example.com",
					"port":     float64(8080),
				},
			},
			expectError: false,
		},
		{
			name:          "SetDeeplyNestedValue",
			initialConfig: map[string]any{},
			path:          "level1.level2.level3",
			value:         "deep-value",
			expectedConfig: map[string]any{
				"level1": map[string]any{
					"level2": map[string]any{
						"level3": "deep-value",
					},
				},
			},
			expectError: false,
		},
		{
			name:          "SetBoolValueTrue",
			initialConfig: map[string]any{},
			path:          "infra.parameters.testBool",
			value:         "true",
			expectedConfig: map[string]any{
				"infra": map[string]any{
					"parameters": map[string]any{
						"testBool": true,
					},
				},
			},
			expectError: false,
		},
		{
			name:          "SetBoolValueFalse",
			initialConfig: map[string]any{},
			path:          "infra.parameters.testBool",
			value:         "false",
			expectedConfig: map[string]any{
				"infra": map[string]any{
					"parameters": map[string]any{
						"testBool": false,
					},
				},
			},
			expectError: false,
		},
		{
			name:          "SetNumberValue",
			initialConfig: map[string]any{},
			path:          "infra.parameters.count",
			value:         "42",
			expectedConfig: map[string]any{
				"infra": map[string]any{
					"parameters": map[string]any{
						"count": float64(42),
					},
				},
			},
			expectError: false,
		},
		{
			name:          "SetArrayValue",
			initialConfig: map[string]any{},
			path:          "infra.parameters.testStrings",
			value:         `["one", "two", "three"]`,
			expectedConfig: map[string]any{
				"infra": map[string]any{
					"parameters": map[string]any{
						"testStrings": []any{"one", "two", "three"},
					},
				},
			},
			expectError: false,
		},
		{
			name:          "SetObjectValue",
			initialConfig: map[string]any{},
			path:          "infra.parameters.tags",
			value:         `{"env":"dev","team":"platform"}`,
			expectedConfig: map[string]any{
				"infra": map[string]any{
					"parameters": map[string]any{
						"tags": map[string]any{"env": "dev", "team": "platform"},
					},
				},
			},
			expectError: false,
		},
		{
			name:          "SetPlainStringValue",
			initialConfig: map[string]any{},
			path:          "app.name",
			value:         "my-app",
			expectedConfig: map[string]any{
				"app": map[string]any{
					"name": "my-app",
				},
			},
			expectError: false,
		},
		{
			name:          "SetNullAsString",
			initialConfig: map[string]any{},
			path:          "app.value",
			value:         "null",
			expectedConfig: map[string]any{
				"app": map[string]any{
					"value": "null",
				},
			},
			expectError: false,
		},
		{
			name:          "SetQuotedJsonForcesStringType",
			initialConfig: map[string]any{},
			path:          "app.flag",
			value:         `"true"`,
			expectedConfig: map[string]any{
				"app": map[string]any{
					"flag": "true",
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envName := "test-env"
			azdCtx, envManager, _ := setupTestEnvironment(t, envName, tt.initialConfig)

			// Setup action
			flags := &envConfigSetFlags{}
			flags.EnvironmentName = envName
			action := newEnvConfigSetAction(
				azdCtx,
				envManager,
				flags,
				[]string{tt.path, tt.value},
			)

			// Run action
			_, err := action.Run(context.Background())

			// Verify results
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)

				// Reload environment and verify config
				reloadedEnv, err := envManager.Get(context.Background(), envName)
				require.NoError(t, err)

				require.Equal(t, tt.expectedConfig, reloadedEnv.Config.Raw())
			}
		})
	}
}

// TestEnvConfigUnset tests the azd env config unset command
func TestEnvConfigUnset(t *testing.T) {
	tests := []struct {
		name           string
		initialConfig  map[string]any
		path           string
		expectedConfig map[string]any
		expectError    bool
	}{
		{
			name: "UnsetSimpleValue",
			initialConfig: map[string]any{
				"key": "value",
			},
			path:           "key",
			expectedConfig: map[string]any{},
			expectError:    false,
		},
		{
			name: "UnsetNestedValue",
			initialConfig: map[string]any{
				"app": map[string]any{
					"endpoint": "https://example.com",
					"port":     "8080",
				},
			},
			path: "app.endpoint",
			expectedConfig: map[string]any{
				"app": map[string]any{
					"port": "8080",
				},
			},
			expectError: false,
		},
		{
			name: "UnsetEntireObject",
			initialConfig: map[string]any{
				"app": map[string]any{
					"endpoint": "https://example.com",
					"port":     "8080",
				},
				"other": "value",
			},
			path: "app",
			expectedConfig: map[string]any{
				"other": "value",
			},
			expectError: false,
		},
		{
			name: "UnsetNonExistentKey",
			initialConfig: map[string]any{
				"key": "value",
			},
			path: "nonexistent",
			expectedConfig: map[string]any{
				"key": "value",
			},
			expectError: false, // Unset is idempotent
		},
		{
			name: "UnsetDeeplyNestedValue",
			initialConfig: map[string]any{
				"level1": map[string]any{
					"level2": map[string]any{
						"level3": "deep-value",
						"other":  "keep",
					},
				},
			},
			path: "level1.level2.level3",
			expectedConfig: map[string]any{
				"level1": map[string]any{
					"level2": map[string]any{
						"other": "keep",
					},
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envName := "test-env"
			azdCtx, envManager, _ := setupTestEnvironment(t, envName, tt.initialConfig)

			// Setup action
			flags := &envConfigUnsetFlags{}
			flags.EnvironmentName = envName
			action := newEnvConfigUnsetAction(
				azdCtx,
				envManager,
				flags,
				[]string{tt.path},
			)

			// Run action
			_, err := action.Run(context.Background())

			// Verify results
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)

				// Reload environment and verify config
				reloadedEnv, err := envManager.Get(context.Background(), envName)
				require.NoError(t, err)

				require.Equal(t, tt.expectedConfig, reloadedEnv.Config.Raw())
			}
		})
	}
}

// TestEnvConfigNonExistentEnvironment tests error handling when environment doesn't exist
func TestEnvConfigNonExistentEnvironment(t *testing.T) {
	tempDir := t.TempDir()
	azdCtx := azdcontext.NewAzdContextWithDirectory(tempDir)

	configManager := config.NewFileConfigManager(config.NewManager())
	localDataStore := environment.NewLocalFileDataStore(azdCtx, configManager)

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.Container.MustRegisterSingleton(func() *azdcontext.AzdContext {
		return azdCtx
	})
	mockContext.Container.MustRegisterSingleton(func() environment.LocalDataStore {
		return localDataStore
	})
	mockContext.Container.MustRegisterSingleton(func() *state.RemoteConfig {
		return nil
	})
	mockContext.Container.MustRegisterSingleton(environment.NewManager)

	var envManager environment.Manager
	err := mockContext.Container.Resolve(&envManager)
	require.NoError(t, err)

	t.Run("GetWithNonExistentEnv", func(t *testing.T) {
		buf := &bytes.Buffer{}
		flags := &envConfigGetFlags{}
		flags.EnvironmentName = "nonexistent"
		action := newEnvConfigGetAction(
			azdCtx,
			envManager,
			&output.JsonFormatter{},
			buf,
			flags,
			[]string{"key"},
		)

		_, err := action.Run(context.Background())
		require.Error(t, err)
		require.Contains(t, err.Error(), "does not exist")
	})

	t.Run("SetWithNonExistentEnv", func(t *testing.T) {
		flags := &envConfigSetFlags{}
		flags.EnvironmentName = "nonexistent"
		action := newEnvConfigSetAction(
			azdCtx,
			envManager,
			flags,
			[]string{"key", "value"},
		)

		_, err := action.Run(context.Background())
		require.Error(t, err)
		require.Contains(t, err.Error(), "does not exist")
	})

	t.Run("UnsetWithNonExistentEnv", func(t *testing.T) {
		flags := &envConfigUnsetFlags{}
		flags.EnvironmentName = "nonexistent"
		action := newEnvConfigUnsetAction(
			azdCtx,
			envManager,
			flags,
			[]string{"key"},
		)

		_, err := action.Run(context.Background())
		require.Error(t, err)
		require.Contains(t, err.Error(), "does not exist")
	})
}

// TestEnvConfigWithDefaultEnvironment tests commands work with default environment
func TestEnvConfigWithDefaultEnvironment(t *testing.T) {
	envName := "default-env"
	azdCtx, envManager, _ := setupTestEnvironment(t, envName, map[string]any{
		"test": "value",
	})

	// Set default environment
	err := azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: envName})
	require.NoError(t, err)

	t.Run("GetWithDefaultEnv", func(t *testing.T) {
		buf := &bytes.Buffer{}
		flags := &envConfigGetFlags{}
		flags.EnvironmentName = "" // Use default
		action := newEnvConfigGetAction(
			azdCtx,
			envManager,
			&output.JsonFormatter{},
			buf,
			flags,
			[]string{"test"},
		)

		_, err := action.Run(context.Background())
		require.NoError(t, err)

		var result any
		err = json.Unmarshal(buf.Bytes(), &result)
		require.NoError(t, err)
		require.Equal(t, "value", result)
	})
}

// TestEnvConfigMultipleOperations tests multiple operations on the same environment
func TestEnvConfigMultipleOperations(t *testing.T) {
	envName := "multi-op-env"
	azdCtx, envManager, _ := setupTestEnvironment(t, envName, map[string]any{})

	// Set multiple values
	setFlags1 := &envConfigSetFlags{}
	setFlags1.EnvironmentName = envName
	setAction1 := newEnvConfigSetAction(
		azdCtx,
		envManager,
		setFlags1,
		[]string{"app.endpoint", "https://example.com"},
	)
	_, err := setAction1.Run(context.Background())
	require.NoError(t, err)

	setFlags2 := &envConfigSetFlags{}
	setFlags2.EnvironmentName = envName
	setAction2 := newEnvConfigSetAction(
		azdCtx,
		envManager,
		setFlags2,
		[]string{"app.port", "8080"},
	)
	_, err = setAction2.Run(context.Background())
	require.NoError(t, err)

	// Verify both values exist
	buf := &bytes.Buffer{}
	getFlags1 := &envConfigGetFlags{}
	getFlags1.EnvironmentName = envName
	getAction := newEnvConfigGetAction(
		azdCtx,
		envManager,
		&output.JsonFormatter{},
		buf,
		getFlags1,
		[]string{"app"},
	)
	_, err = getAction.Run(context.Background())
	require.NoError(t, err)

	var result map[string]any
	err = json.Unmarshal(buf.Bytes(), &result)
	require.NoError(t, err)
	require.Equal(t, "https://example.com", result["endpoint"])
	require.Equal(t, float64(8080), result["port"])

	// Unset one value
	unsetFlags := &envConfigUnsetFlags{}
	unsetFlags.EnvironmentName = envName
	unsetAction := newEnvConfigUnsetAction(
		azdCtx,
		envManager,
		unsetFlags,
		[]string{"app.endpoint"},
	)
	_, err = unsetAction.Run(context.Background())
	require.NoError(t, err)

	// Verify only port remains
	buf = &bytes.Buffer{}
	getFlags2 := &envConfigGetFlags{}
	getFlags2.EnvironmentName = envName
	getAction2 := newEnvConfigGetAction(
		azdCtx,
		envManager,
		&output.JsonFormatter{},
		buf,
		getFlags2,
		[]string{"app"},
	)
	_, err = getAction2.Run(context.Background())
	require.NoError(t, err)

	var result2 map[string]any
	err = json.Unmarshal(buf.Bytes(), &result2)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"port": float64(8080)}, result2)
}
