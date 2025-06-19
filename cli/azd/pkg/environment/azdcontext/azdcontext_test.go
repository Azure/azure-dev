// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdcontext

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectFileNameForEnvironment(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "azd-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create azd context
	azdCtx := &AzdContext{
		projectDirectory: tempDir,
	}

	// Create .azure directory and environment configs
	azdDir := filepath.Join(tempDir, EnvironmentDirectoryName)
	require.NoError(t, os.MkdirAll(azdDir, 0755))

	// Test default environment behavior (should use azure.yaml when no default environment)
	t.Run("no default environment", func(t *testing.T) {
		fileName := azdCtx.ProjectFileNameForEnvironment("")
		assert.Equal(t, "azure.yaml", fileName)
	})

	// Create a dev environment with type
	devEnvDir := filepath.Join(azdDir, "dev")
	require.NoError(t, os.MkdirAll(devEnvDir, 0755))
	devConfigPath := filepath.Join(devEnvDir, ConfigFileName)
	devConfig := `{"version": 1, "environmentType": "dev"}`
	require.NoError(t, os.WriteFile(devConfigPath, []byte(devConfig), 0644))

	// Create a prod environment with type
	prodEnvDir := filepath.Join(azdDir, "prod")
	require.NoError(t, os.MkdirAll(prodEnvDir, 0755))
	prodConfigPath := filepath.Join(prodEnvDir, ConfigFileName)
	prodConfig := `{"version": 1, "environmentType": "prod"}`
	require.NoError(t, os.WriteFile(prodConfigPath, []byte(prodConfig), 0644))

	// Test dev environment
	t.Run("dev environment", func(t *testing.T) {
		fileName := azdCtx.ProjectFileNameForEnvironment("dev")
		assert.Equal(t, "azure.dev.yaml", fileName)
	})

	// Test prod environment
	t.Run("prod environment", func(t *testing.T) {
		fileName := azdCtx.ProjectFileNameForEnvironment("prod")
		assert.Equal(t, "azure.prod.yaml", fileName)
	})

	// Test non-existent environment
	t.Run("non-existent environment", func(t *testing.T) {
		fileName := azdCtx.ProjectFileNameForEnvironment("nonexistent")
		assert.Equal(t, "azure.yaml", fileName)
	})

	// Test with default environment set
	configPath := filepath.Join(azdDir, ConfigFileName)
	config := `{"version": 2, "defaultEnvironment": "dev"}`
	require.NoError(t, os.WriteFile(configPath, []byte(config), 0644))

	t.Run("default environment set", func(t *testing.T) {
		fileName := azdCtx.ProjectFileNameForEnvironment("")
		assert.Equal(t, "azure.dev.yaml", fileName)
	})
}

func TestProjectPathForEnvironment(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "azd-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create azd context
	azdCtx := &AzdContext{
		projectDirectory: tempDir,
	}

	// Create .azure directory and environment configs
	azdDir := filepath.Join(tempDir, EnvironmentDirectoryName)
	require.NoError(t, os.MkdirAll(azdDir, 0755))

	// Create a prod environment with type
	prodEnvDir := filepath.Join(azdDir, "prod")
	require.NoError(t, os.MkdirAll(prodEnvDir, 0755))
	prodConfigPath := filepath.Join(prodEnvDir, ConfigFileName)
	prodConfig := `{"version": 1, "environmentType": "prod"}`
	require.NoError(t, os.WriteFile(prodConfigPath, []byte(prodConfig), 0644))

	// Test environment-specific project path
	t.Run("prod environment path", func(t *testing.T) {
		projectPath := azdCtx.ProjectPathForEnvironment("prod")
		expectedPath := filepath.Join(tempDir, "azure.prod.yaml")
		assert.Equal(t, expectedPath, projectPath)
	})

	// Test default project path
	t.Run("default project path", func(t *testing.T) {
		projectPath := azdCtx.ProjectPath()
		expectedPath := filepath.Join(tempDir, "azure.yaml")
		assert.Equal(t, expectedPath, projectPath)
	})
}
