// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_LocalFileDataStore_List(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	dataStore := NewLocalFileDataStore(azdContext, fileConfigManager)

	t.Run("List", func(t *testing.T) {
		env1 := New("env1")
		err := dataStore.Save(*mockContext.Context, env1, nil)
		require.NoError(t, err)

		env2 := New("env2")
		err = dataStore.Save(*mockContext.Context, env2, nil)
		require.NoError(t, err)

		envList, err := dataStore.List(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, envList)
		require.Equal(t, 2, len(envList))
	})

	t.Run("Empty", func(t *testing.T) {
		envList, err := dataStore.List(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, envList)
	})

	t.Run("ValidatesEnvironmentNames", func(t *testing.T) {
		// Create a fresh temporary directory for this test to avoid interference
		tempDir := t.TempDir()
		freshAzdContext := azdcontext.NewAzdContextWithDirectory(tempDir)
		freshDataStore := NewLocalFileDataStore(freshAzdContext, fileConfigManager)

		// Create an environment with a valid name
		validEnv := New("valid-env")
		err := freshDataStore.Save(*mockContext.Context, validEnv, nil)
		require.NoError(t, err)

		// Manually create a directory with an invalid name
		invalidEnvDir := filepath.Join(freshAzdContext.EnvironmentDirectory(), "#invalid$name")
		err = os.MkdirAll(invalidEnvDir, 0755)
		require.NoError(t, err)

		// List environments
		envList, err := freshDataStore.List(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, envList)
		require.Equal(t, 2, len(envList))

		// Check that valid environment has IsValid=true
		var validEnvResult *contracts.EnvListEnvironment
		var invalidEnvResult *contracts.EnvListEnvironment
		for _, env := range envList {
			if env.Name == "valid-env" {
				validEnvResult = env
			} else if env.Name == "#invalid$name" {
				invalidEnvResult = env
			}
		}

		require.NotNil(t, validEnvResult, "valid-env should be in the list")
		require.True(t, validEnvResult.IsValid, "valid-env should be marked as valid")

		require.NotNil(t, invalidEnvResult, "#invalid$name should be in the list")
		require.False(t, invalidEnvResult.IsValid, "#invalid$name should be marked as invalid")
	})
}

func Test_LocalFileDataStore_SaveAndGet(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	dataStore := NewLocalFileDataStore(azdContext, fileConfigManager)

	t.Run("Success", func(t *testing.T) {
		env1 := New("env1")
		env1.DotenvSet("key1", "value1")
		err := dataStore.Save(*mockContext.Context, env1, nil)
		require.NoError(t, err)

		env, err := dataStore.Get(*mockContext.Context, "env1")
		require.NoError(t, err)
		require.NotNil(t, env)
		require.Equal(t, "env1", env.name)
		actual := env1.Getenv("key1")
		require.Equal(t, "value1", actual)
	})
}

func Test_LocalFileDataStore_Path(t *testing.T) {
	azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	dataStore := NewLocalFileDataStore(azdContext, fileConfigManager)

	env := New("env1")
	expected := filepath.Join(azdContext.EnvironmentRoot("env1"), DotEnvFileName)
	actual := dataStore.EnvPath(env)

	require.Equal(t, expected, actual)
}

func Test_LocalFileDataStore_ConfigPath(t *testing.T) {
	azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	dataStore := NewLocalFileDataStore(azdContext, fileConfigManager)

	env := New("env1")
	expected := filepath.Join(azdContext.EnvironmentRoot("env1"), ConfigFileName)
	actual := dataStore.ConfigPath(env)

	require.Equal(t, expected, actual)
}
