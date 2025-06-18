// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdcontext

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestEnv creates a test environment with the given name and environment type
func setupTestEnv(t *testing.T, azdDir, envName, envType string) {
	t.Helper()

	envDir := filepath.Join(azdDir, envName)
	require.NoError(t, os.MkdirAll(envDir, osutil.PermissionDirectory))

	envPath := filepath.Join(envDir, DotEnvFileName)
	content := "AZURE_ENV_NAME=" + envName + "\n"
	if envType != "" {
		content += "AZURE_ENV_TYPE=" + envType + "\n"
	}
	require.NoError(t, os.WriteFile(envPath, []byte(content), osutil.PermissionFileOwnerOnly))
}

// setupDefaultEnv sets up the default environment configuration
func setupDefaultEnv(t *testing.T, azdDir, defaultEnvName string) {
	t.Helper()

	configPath := filepath.Join(azdDir, ConfigFileName)
	config := `{"version": 1, "defaultEnvironment": "` + defaultEnvName + `"}`
	require.NoError(t, os.WriteFile(configPath, []byte(config), osutil.PermissionFileOwnerOnly))
}

func TestProjectFileNameForEnvironment(t *testing.T) {
	tests := []struct {
		name       string
		envName    string
		envType    string
		defaultEnv string
		expected   string
	}{
		{
			name:     "no default environment",
			envName:  "",
			expected: "azure.yaml",
		},
		{
			name:     "dev type",
			envName:  "TESTENV",
			envType:  "dev",
			expected: "azure.dev.yaml",
		},
		{
			name:     "prod type",
			envName:  "TESTENV",
			envType:  "prod",
			expected: "azure.prod.yaml",
		},
		{
			name:     "non-existent environment",
			envName:  "nonexistent",
			expected: "azure.yaml",
		},
		{
			name:     "existing environment without type",
			envName:  "TESTENV",
			envType:  "", // explicitly no type
			expected: "azure.yaml",
		},
		{
			name:       "default environment set",
			envName:    "",
			envType:    "dev",
			defaultEnv: "TESTENV1",
			expected:   "azure.dev.yaml",
		},
		{
			name:       "default environment without type",
			envName:    "",
			envType:    "", // default env has no type
			defaultEnv: "TESTENV",
			expected:   "azure.yaml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			azdCtx := NewAzdContextWithDirectory(t.TempDir())
			azdDir := filepath.Join(azdCtx.ProjectDirectory(), EnvironmentDirectoryName)
			require.NoError(t, os.MkdirAll(azdDir, osutil.PermissionDirectory))

			// Setup test environment if specified
			if tt.envName != "" {
				setupTestEnv(t, azdDir, tt.envName, tt.envType)
			}
			if tt.defaultEnv != "" && tt.envName == "" {
				setupTestEnv(t, azdDir, tt.defaultEnv, tt.envType)
				setupDefaultEnv(t, azdDir, tt.defaultEnv)
			}

			fileName := azdCtx.ProjectFileNameForEnvironment(tt.envName)
			assert.Equal(t, tt.expected, fileName)
		})
	}
}

func TestProjectPathForEnvironment(t *testing.T) {
	tests := []struct {
		name     string
		envName  string
		envType  string
		expected string
	}{
		{
			name:     "environment with type",
			envName:  "TESTENV1",
			envType:  "prod",
			expected: "azure.prod.yaml",
		},
		{
			name:     "default project path",
			envName:  "",
			expected: "azure.yaml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			azdCtx := NewAzdContextWithDirectory(t.TempDir())
			azdDir := filepath.Join(azdCtx.ProjectDirectory(), EnvironmentDirectoryName)
			require.NoError(t, os.MkdirAll(azdDir, osutil.PermissionDirectory))

			if tt.envType != "" {
				setupTestEnv(t, azdDir, tt.envName, tt.envType)
			}

			var projectPath string
			if tt.envName == "" {
				projectPath = azdCtx.ProjectPath()
			} else {
				projectPath = azdCtx.ProjectPathForEnvironment(tt.envName)
			}

			expectedPath := filepath.Join(azdCtx.ProjectDirectory(), tt.expected)
			assert.Equal(t, expectedPath, projectPath)
		})
	}
}

func TestGetEnvironmentType(t *testing.T) {
	tests := []struct {
		name           string
		envName        string
		envType        string
		setupDefault   bool
		defaultEnvName string
		expected       string
	}{
		{
			name:     "non-existent environment",
			envName:  "nonexistent",
			expected: "",
		},
		{
			name:     "existing environment with type",
			envName:  "TESTENV",
			envType:  "dev",
			expected: "dev",
		},
		{
			name:     "existing environment without type",
			envName:  "TESTENV",
			envType:  "",
			expected: "",
		},
		{
			name:           "default environment with type",
			envName:        "",
			envType:        "dev",
			setupDefault:   true,
			defaultEnvName: "TESTENV",
			expected:       "dev",
		},
		{
			name:     "no default environment",
			envName:  "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			azdCtx := NewAzdContextWithDirectory(t.TempDir())
			azdDir := filepath.Join(azdCtx.ProjectDirectory(), EnvironmentDirectoryName)
			require.NoError(t, os.MkdirAll(azdDir, osutil.PermissionDirectory))

			// Setup environment if needed
			if tt.envName != "" || tt.defaultEnvName != "" {
				envName := tt.envName
				if envName == "" {
					envName = tt.defaultEnvName
				}
				setupTestEnv(t, azdDir, envName, tt.envType)
			}

			// Setup default environment if needed
			if tt.setupDefault {
				setupDefaultEnv(t, azdDir, tt.defaultEnvName)
			}

			envType, err := azdCtx.GetEnvironmentType(tt.envName)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, envType)
		})
	}
}
