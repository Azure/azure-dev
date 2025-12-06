// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdcontext

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewAzdContextFromWd_WithAzureYaml(t *testing.T) {
	tempDir := t.TempDir()

	// Create azure.yaml file
	azureYamlPath := filepath.Join(tempDir, "azure.yaml")
	err := os.WriteFile(azureYamlPath, []byte("name: test\n"), 0600)
	require.NoError(t, err)

	// Test from the directory containing azure.yaml
	ctx, err := NewAzdContextFromWd(tempDir)
	require.NoError(t, err)
	require.NotNil(t, ctx)
	require.Equal(t, tempDir, ctx.ProjectDirectory())
	require.Equal(t, azureYamlPath, ctx.ProjectPath())
}

func TestNewAzdContextFromWd_WithAzureYml(t *testing.T) {
	tempDir := t.TempDir()

	// Create azure.yml file
	azureYmlPath := filepath.Join(tempDir, "azure.yml")
	err := os.WriteFile(azureYmlPath, []byte("name: test\n"), 0600)
	require.NoError(t, err)

	// Test from the directory containing azure.yml
	ctx, err := NewAzdContextFromWd(tempDir)
	require.NoError(t, err)
	require.NotNil(t, ctx)
	require.Equal(t, tempDir, ctx.ProjectDirectory())
	require.Equal(t, azureYmlPath, ctx.ProjectPath())
}

func TestNewAzdContextFromWd_BothFilesExist_YamlTakesPrecedence(t *testing.T) {
	tempDir := t.TempDir()

	// Create both azure.yaml and azure.yml
	azureYamlPath := filepath.Join(tempDir, "azure.yaml")
	azureYmlPath := filepath.Join(tempDir, "azure.yml")
	err := os.WriteFile(azureYamlPath, []byte("name: yaml\n"), 0600)
	require.NoError(t, err)
	err = os.WriteFile(azureYmlPath, []byte("name: yml\n"), 0600)
	require.NoError(t, err)

	// Test that azure.yaml takes precedence
	ctx, err := NewAzdContextFromWd(tempDir)
	require.NoError(t, err)
	require.NotNil(t, ctx)
	require.Equal(t, tempDir, ctx.ProjectDirectory())
	require.Equal(t, azureYamlPath, ctx.ProjectPath(), "azure.yaml should take precedence over azure.yml")
}

func TestNewAzdContextFromWd_FromSubdirectory(t *testing.T) {
	tempDir := t.TempDir()
	subDir := filepath.Join(tempDir, "src", "api")
	err := os.MkdirAll(subDir, 0755)
	require.NoError(t, err)

	// Create azure.yml in the root
	azureYmlPath := filepath.Join(tempDir, "azure.yml")
	err = os.WriteFile(azureYmlPath, []byte("name: test\n"), 0600)
	require.NoError(t, err)

	// Test from subdirectory - should walk up and find the file
	ctx, err := NewAzdContextFromWd(subDir)
	require.NoError(t, err)
	require.NotNil(t, ctx)
	require.Equal(t, tempDir, ctx.ProjectDirectory())
	require.Equal(t, azureYmlPath, ctx.ProjectPath())
}

func TestNewAzdContextFromWd_NoProjectFile(t *testing.T) {
	tempDir := t.TempDir()

	// No project file exists
	ctx, err := NewAzdContextFromWd(tempDir)
	require.Error(t, err)
	require.Nil(t, ctx)
	require.ErrorIs(t, err, ErrNoProject)
}

func TestNewAzdContextFromWd_DirectoryWithSameName(t *testing.T) {
	tempDir := t.TempDir()

	// Create a directory named azure.yaml (edge case)
	azureYamlDir := filepath.Join(tempDir, "azure.yaml")
	err := os.Mkdir(azureYamlDir, 0755)
	require.NoError(t, err)

	// Create actual azure.yml file
	azureYmlPath := filepath.Join(tempDir, "azure.yml")
	err = os.WriteFile(azureYmlPath, []byte("name: test\n"), 0600)
	require.NoError(t, err)

	// Should find azure.yml and skip the directory
	ctx, err := NewAzdContextFromWd(tempDir)
	require.NoError(t, err)
	require.NotNil(t, ctx)
	require.Equal(t, tempDir, ctx.ProjectDirectory())
	require.Equal(t, azureYmlPath, ctx.ProjectPath())
}

func TestNewAzdContextFromWd_InvalidPath(t *testing.T) {
	// Test with a path that doesn't exist
	ctx, err := NewAzdContextFromWd("/this/path/does/not/exist/at/all")
	require.Error(t, err)
	require.Nil(t, ctx)
}

func TestProjectPath_WithFoundFile(t *testing.T) {
	tempDir := t.TempDir()
	azureYmlPath := filepath.Join(tempDir, "azure.yml")
	err := os.WriteFile(azureYmlPath, []byte("name: test\n"), 0600)
	require.NoError(t, err)

	ctx, err := NewAzdContextFromWd(tempDir)
	require.NoError(t, err)

	// ProjectPath should return the actual found file
	require.Equal(t, azureYmlPath, ctx.ProjectPath())
}

func TestProjectPath_WithoutFoundFile(t *testing.T) {
	// Create context directly without searching
	ctx := NewAzdContextWithDirectory("/some/path")

	// ProjectPath should return default azure.yaml
	expected := filepath.Join("/some/path", "azure.yaml")
	require.Equal(t, expected, ctx.ProjectPath())
}

func TestNewAzdContextWithDirectory(t *testing.T) {
	testDir := "/test/directory"
	ctx := NewAzdContextWithDirectory(testDir)

	require.NotNil(t, ctx)
	require.Equal(t, testDir, ctx.ProjectDirectory())
	require.Equal(t, filepath.Join(testDir, "azure.yaml"), ctx.ProjectPath())
}

func TestSetProjectDirectory(t *testing.T) {
	ctx := NewAzdContextWithDirectory("/original/path")
	require.Equal(t, "/original/path", ctx.ProjectDirectory())

	ctx.SetProjectDirectory("/new/path")
	require.Equal(t, "/new/path", ctx.ProjectDirectory())
}

func TestEnvironmentDirectory(t *testing.T) {
	ctx := NewAzdContextWithDirectory("/test/path")
	expected := filepath.Join("/test/path", ".azure")
	require.Equal(t, expected, ctx.EnvironmentDirectory())
}

func TestEnvironmentRoot(t *testing.T) {
	ctx := NewAzdContextWithDirectory("/test/path")
	expected := filepath.Join("/test/path", ".azure", "env1")
	require.Equal(t, expected, ctx.EnvironmentRoot("env1"))
}

func TestGetEnvironmentWorkDirectory(t *testing.T) {
	ctx := NewAzdContextWithDirectory("/test/path")
	expected := filepath.Join("/test/path", ".azure", "env1", "wd")
	require.Equal(t, expected, ctx.GetEnvironmentWorkDirectory("env1"))
}

func TestProjectFileNames_Order(t *testing.T) {
	// Verify the order of preference
	require.Len(t, ProjectFileNames, 2)
	require.Equal(t, "azure.yaml", ProjectFileNames[0], "azure.yaml should be first (highest priority)")
	require.Equal(t, "azure.yml", ProjectFileNames[1], "azure.yml should be second")
}

func TestProjectName(t *testing.T) {
	tests := []struct {
		name     string
		inputDir string
		expected string
	}{
		{
			name:     "Simple directory name",
			inputDir: "/home/user/my-project",
			expected: "my-project",
		},
		{
			name:     "Directory with special characters",
			inputDir: "/home/user/My_Project-123",
			expected: "my-project-123",
		},
		{
			name:     "Root directory",
			inputDir: "/",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ProjectName(tt.inputDir)
			require.Equal(t, tt.expected, result)
		})
	}
}
