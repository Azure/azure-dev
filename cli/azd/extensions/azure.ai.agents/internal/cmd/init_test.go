// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestContext holds all mock objects needed for init command tests
type TestContext struct {
	Ctx     context.Context
	TempDir string
	Action  *InitAction
}

// NewTestContext creates a new test context with initialized mocks
func NewTestContext(t *testing.T) *TestContext {
	t.Helper()

	tempDir, err := os.MkdirTemp("", "init_test_*")
	require.NoError(t, err)

	// Create a minimal InitAction for testing helper methods
	// Note: azdClient and other dependencies are nil - only for testing helper methods
	action := &InitAction{}

	return &TestContext{
		Ctx:     context.Background(),
		TempDir: tempDir,
		Action:  action,
	}
}

// Cleanup removes temporary files created during testing
func (tc *TestContext) Cleanup() {
	if tc.TempDir != "" {
		os.RemoveAll(tc.TempDir)
	}
}

// CreateTempFile creates a temporary file with the given content
func (tc *TestContext) CreateTempFile(t *testing.T, filename string, content string) string {
	t.Helper()

	filePath := filepath.Join(tc.TempDir, filename)
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	return filePath
}

// Sample valid agent manifest YAML for testing
const validAgentManifestYAML = `
name: TestAgent
description: A test agent for unit testing
template:
  kind: prompt
  name: TestAgent
  description: A test agent for unit testing
  model:
    id: gpt-4o
    publisher: azure
  instructions: |
    You are a helpful test assistant.
`

// Sample minimal agent manifest
const minimalAgentManifestYAML = `
name: MinimalAgent
template:
  kind: prompt
  name: MinimalAgent
  model:
    id: gpt-4o
    publisher: azure
  instructions: You are a minimal test agent.
`

// TestIsLocalFilePath tests the isLocalFilePath helper method
func TestIsLocalFilePath(t *testing.T) {
	tc := NewTestContext(t)
	defer tc.Cleanup()

	// Create a real file for the "existing file" test case
	existingFile := tc.CreateTempFile(t, "agent.yaml", validAgentManifestYAML)

	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{
			name:     "HttpUrl",
			path:     "http://example.com/agent.yaml",
			expected: false,
		},
		{
			name:     "HttpsUrl",
			path:     "https://example.com/agent.yaml",
			expected: false,
		},
		{
			name:     "GitHubUrl",
			path:     "https://github.com/owner/repo/blob/main/agent.yaml",
			expected: false,
		},
		{
			name:     "ExistingLocalFile",
			path:     existingFile,
			expected: true,
		},
		{
			name:     "NonExistentLocalPath",
			path:     filepath.Join(tc.TempDir, "nonexistent.yaml"),
			expected: false,
		},
		{
			name:     "RelativePathNonExistent",
			path:     "./some/relative/path.yaml",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tc.Action.isLocalFilePath(tt.path)
			require.Equal(t, tt.expected, result)
		})
	}
}

// TestIsGitHubUrl tests the isGitHubUrl helper method
func TestIsGitHubUrl(t *testing.T) {
	tc := NewTestContext(t)
	defer tc.Cleanup()

	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		{
			name:     "GitHubBlobUrl",
			url:      "https://github.com/owner/repo/blob/main/file.yaml",
			expected: true,
		},
		{
			name:     "GitHubRawUrl",
			url:      "https://raw.githubusercontent.com/owner/repo/main/file.yaml",
			expected: true,
		},
		{
			name:     "GitHubApiUrl",
			url:      "https://api.github.com/repos/owner/repo/contents/file.yaml",
			expected: true,
		},
		{
			name:     "GitHubEnterpriseUrl",
			url:      "https://github.mycompany.com/owner/repo/blob/main/file.yaml",
			expected: true,
		},
		{
			name:     "NonGitHubUrl",
			url:      "https://example.com/file.yaml",
			expected: false,
		},
		{
			name:     "AzureMLRegistryUrl",
			url:      "azureml://registries/myregistry/agentmanifests/myagent",
			expected: false,
		},
		{
			name:     "InvalidUrl",
			url:      "not a valid url ::::",
			expected: false,
		},
		{
			name:     "EmptyString",
			url:      "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tc.Action.isGitHubUrl(tt.url)
			require.Equal(t, tt.expected, result)
		})
	}
}

// TestIsRegistryUrl tests the isRegistryUrl helper method
func TestIsRegistryUrl(t *testing.T) {
	tc := NewTestContext(t)
	defer tc.Cleanup()

	tests := []struct {
		name             string
		url              string
		expectedValid    bool
		expectedRegistry string
		expectedName     string
		expectedVersion  string
	}{
		{
			name:             "ValidWithVersion",
			url:              "azureml://registries/myregistry/agentmanifests/myagent/versions/1.0.0",
			expectedValid:    true,
			expectedRegistry: "myregistry",
			expectedName:     "myagent",
			expectedVersion:  "1.0.0",
		},
		{
			name:             "ValidWithoutVersion",
			url:              "azureml://registries/myregistry/agentmanifests/myagent",
			expectedValid:    true,
			expectedRegistry: "myregistry",
			expectedName:     "myagent",
			expectedVersion:  "",
		},
		{
			name:             "ValidWithComplexNames",
			url:              "azureml://registries/my-registry-name/agentmanifests/my-agent-name/versions/2.1.0-beta",
			expectedValid:    true,
			expectedRegistry: "my-registry-name",
			expectedName:     "my-agent-name",
			expectedVersion:  "2.1.0-beta",
		},
		{
			name:          "InvalidPrefix",
			url:           "https://registries/myregistry/agentmanifests/myagent",
			expectedValid: false,
		},
		{
			name:          "InvalidPath_TooFewParts",
			url:           "azureml://registries/myregistry",
			expectedValid: false,
		},
		{
			name:          "InvalidPath_WrongKeyword",
			url:           "azureml://registries/myregistry/manifests/myagent",
			expectedValid: false,
		},
		{
			name:          "InvalidPath_FivePartsInvalid",
			url:           "azureml://registries/myregistry/agentmanifests/myagent/extra",
			expectedValid: false,
		},
		{
			name:          "EmptyRegistryName",
			url:           "azureml://registries//agentmanifests/myagent",
			expectedValid: false,
		},
		{
			name:          "EmptyManifestName",
			url:           "azureml://registries/myregistry/agentmanifests/",
			expectedValid: false,
		},
		{
			name:          "EmptyVersionWithKeyword",
			url:           "azureml://registries/myregistry/agentmanifests/myagent/versions/",
			expectedValid: false,
		},
		{
			name:          "WhitespaceOnly",
			url:           "azureml://registries/  /agentmanifests/myagent",
			expectedValid: false,
		},
		{
			name:          "GitHubUrl",
			url:           "https://github.com/owner/repo/blob/main/agent.yaml",
			expectedValid: false,
		},
		{
			name:          "EmptyString",
			url:           "",
			expectedValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isValid, manifest := tc.Action.isRegistryUrl(tt.url)
			require.Equal(t, tt.expectedValid, isValid)

			if tt.expectedValid {
				require.NotNil(t, manifest)
				require.Equal(t, tt.expectedRegistry, manifest.registryName)
				require.Equal(t, tt.expectedName, manifest.manifestName)
				require.Equal(t, tt.expectedVersion, manifest.manifestVersion)
			} else {
				require.Nil(t, manifest)
			}
		})
	}
}

// TestDownloadAgentYaml_EmptyManifestPointer tests that empty manifest pointer returns error
func TestDownloadAgentYaml_EmptyManifestPointer(t *testing.T) {
	tc := NewTestContext(t)
	defer tc.Cleanup()

	_, _, err := tc.Action.downloadAgentYaml(tc.Ctx, "", tc.TempDir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "manifestPointer cannot be empty")
}

// TestDownloadAgentYaml_UnrecognizedFormat tests that unrecognized URL formats return error
func TestDownloadAgentYaml_UnrecognizedFormat(t *testing.T) {
	tc := NewTestContext(t)
	defer tc.Cleanup()

	tests := []struct {
		name            string
		manifestPointer string
	}{
		{
			name:            "RandomHttpsUrl",
			manifestPointer: "https://example.com/some/path/agent.yaml",
		},
		{
			name:            "FtpUrl",
			manifestPointer: "ftp://files.example.com/agent.yaml",
		},
		{
			name:            "MalformedRegistryUrl",
			manifestPointer: "azureml://invalid/format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := tc.Action.downloadAgentYaml(tc.Ctx, tt.manifestPointer, tc.TempDir)
			require.Error(t, err)
			require.Contains(t, err.Error(), "unrecognized manifest pointer format")
		})
	}
}

// TestDownloadAgentYaml_LocalFile_Valid tests downloading from valid local file paths
// NOTE: This test requires mocking azdClient and other dependencies
// For now, we test the helper methods directly and skip the full integration
func TestDownloadAgentYaml_LocalFile_Valid(t *testing.T) {
	t.Skip("Requires mocking azdClient - downloadAgentYaml processes manifest which needs azdClient")

	tc := NewTestContext(t)
	defer tc.Cleanup()

	// Create the local manifest file
	manifestPath := tc.CreateTempFile(t, "agent.yaml", validAgentManifestYAML)

	manifest, _, err := tc.Action.downloadAgentYaml(tc.Ctx, manifestPath, tc.TempDir)
	require.NoError(t, err)
	require.NotNil(t, manifest)
	require.Equal(t, "TestAgent", manifest.Name)
}

// TestDownloadAgentYaml_LocalFile_InvalidYAML tests error handling for invalid YAML
func TestDownloadAgentYaml_LocalFile_InvalidYAML(t *testing.T) {
	t.Skip("Requires mocking azdClient - downloadAgentYaml processes manifest which needs azdClient")

	tc := NewTestContext(t)
	defer tc.Cleanup()

	invalidYAML := `
name: BadAgent
  invalid: indentation
    broken: yaml
`
	manifestPath := tc.CreateTempFile(t, "invalid.yaml", invalidYAML)

	_, _, err := tc.Action.downloadAgentYaml(tc.Ctx, manifestPath, tc.TempDir)
	require.Error(t, err)
	// Error should be about YAML parsing
	require.Contains(t, err.Error(), "YAML")
}

// TestDownloadAgentYaml_LocalFile_NonExistent tests error handling for non-existent files
func TestDownloadAgentYaml_LocalFile_NonExistent(t *testing.T) {
	tc := NewTestContext(t)
	defer tc.Cleanup()

	nonExistentPath := filepath.Join(tc.TempDir, "nonexistent", "agent.yaml")

	// Since isLocalFilePath returns false for non-existent files,
	// this should fail with "unrecognized manifest pointer format"
	_, _, err := tc.Action.downloadAgentYaml(tc.Ctx, nonExistentPath, tc.TempDir)
	require.Error(t, err)
}
