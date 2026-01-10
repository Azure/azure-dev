// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTarGzSource(t *testing.T) {
	// Create a temporary directory for the test
	tempDir, err := os.MkdirTemp("", "test-tar-gz-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create test files
	testFile1 := filepath.Join(tempDir, "test1.txt")
	testFile2 := filepath.Join(tempDir, "test2.txt")
	targetTarGz := filepath.Join(tempDir, "test.tar.gz")

	require.NoError(t, os.WriteFile(testFile1, []byte("content1"), 0600))
	require.NoError(t, os.WriteFile(testFile2, []byte("content2"), 0600))

	// Create tar.gz archive
	files := []string{testFile1, testFile2}
	err = TarGzSource(files, targetTarGz)
	require.NoError(t, err)

	// Verify the tar.gz file was created
	_, err = os.Stat(targetTarGz)
	require.NoError(t, err)

	// Verify the contents of the tar.gz file
	file, err := os.Open(targetTarGz)
	require.NoError(t, err)
	defer file.Close()

	gzReader, err := gzip.NewReader(file)
	require.NoError(t, err)
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)

	// Check first file
	header, err := tarReader.Next()
	require.NoError(t, err)
	require.Equal(t, "test1.txt", header.Name)

	content, err := io.ReadAll(tarReader)
	require.NoError(t, err)
	require.Equal(t, "content1", string(content))

	// Check second file
	header, err = tarReader.Next()
	require.NoError(t, err)
	require.Equal(t, "test2.txt", header.Name)

	content, err = io.ReadAll(tarReader)
	require.NoError(t, err)
	require.Equal(t, "content2", string(content))

	// Verify end of archive
	_, err = tarReader.Next()
	require.Equal(t, io.EOF, err)
}

func TestInferOSArch(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		expected string
		wantErr  bool
	}{
		{
			name:     "Windows executable",
			filename: "microsoft-azd-extensions-windows-amd64.exe",
			expected: "windows/amd64",
			wantErr:  false,
		},
		{
			name:     "Linux tar.gz",
			filename: "microsoft-azd-extensions-linux-amd64.tar.gz",
			expected: "linux/amd64",
			wantErr:  false,
		},
		{
			name:     "Linux arm64 tar.gz",
			filename: "microsoft-azd-extensions-linux-arm64.tar.gz",
			expected: "linux/arm64",
			wantErr:  false,
		},
		{
			name:     "Darwin zip",
			filename: "microsoft-azd-extensions-darwin-amd64.zip",
			expected: "darwin/amd64",
			wantErr:  false,
		},
		{
			name:     "Invalid format",
			filename: "invalid",
			expected: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := InferOSArch(tt.filename)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestFindArtifacts(t *testing.T) {
	// Create a temporary directory for the test
	tempDir, err := os.MkdirTemp("", "test-glob-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create test files
	zipFile := filepath.Join(tempDir, "test-windows-amd64.zip")
	tarGzFile := filepath.Join(tempDir, "test-linux-amd64.tar.gz")
	otherFile := filepath.Join(tempDir, "test.txt")

	require.NoError(t, os.WriteFile(zipFile, []byte("zip content"), 0600))
	require.NoError(t, os.WriteFile(tarGzFile, []byte("tar.gz content"), 0600))
	require.NoError(t, os.WriteFile(otherFile, []byte("other content"), 0600))

	tests := []struct {
		name        string
		patterns    []string
		extensionId string
		version     string
		expected    []string
		wantErr     bool
	}{
		{
			name:        "Concrete file path - zip",
			patterns:    []string{zipFile},
			extensionId: "test.extension",
			version:     "1.0.0",
			expected:    []string{zipFile},
			wantErr:     false,
		},
		{
			name:        "Concrete file path - tar.gz",
			patterns:    []string{tarGzFile},
			extensionId: "test.extension",
			version:     "1.0.0",
			expected:    []string{tarGzFile},
			wantErr:     false,
		},
		{
			name:        "Concrete file path - non-existent",
			patterns:    []string{filepath.Join(tempDir, "nonexistent.zip")},
			extensionId: "test.extension",
			version:     "1.0.0",
			expected:    []string{},
			wantErr:     false,
		},
		{
			name:        "Multiple patterns",
			patterns:    []string{filepath.Join(tempDir, "*.zip"), filepath.Join(tempDir, "*.tar.gz")},
			extensionId: "test.extension",
			version:     "1.0.0",
			expected:    []string{zipFile, tarGzFile},
			wantErr:     false,
		},
		{
			name:        "Single glob pattern",
			patterns:    []string{filepath.Join(tempDir, "*.zip")},
			extensionId: "test.extension",
			version:     "1.0.0",
			expected:    []string{zipFile},
			wantErr:     false,
		},
		{
			name:        "Mixed concrete and pattern",
			patterns:    []string{zipFile, filepath.Join(tempDir, "*.tar.gz")},
			extensionId: "test.extension",
			version:     "1.0.0",
			expected:    []string{zipFile, tarGzFile},
			wantErr:     false,
		},
		{
			name:        "Empty patterns uses defaults",
			patterns:    []string{},
			extensionId: "test.extension",
			version:     "1.0.0",
			expected:    []string{}, // No files in default registry location
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := FindArtifacts(tt.patterns, tt.extensionId, tt.version)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			// Sort both slices for comparison since order might vary
			require.ElementsMatch(t, tt.expected, result)
		})
	}
}

func TestDefaultArtifactPatterns(t *testing.T) {
	patterns, err := DefaultArtifactPatterns("test.extension", "1.0.0")
	require.NoError(t, err)
	require.Len(t, patterns, 2)
	require.Contains(t, patterns[0], "*.zip")
	require.Contains(t, patterns[1], "*.tar.gz")
}

func TestGetFileNameWithoutExt(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     ".exe file",
			input:    "program.exe",
			expected: "program",
		},
		{
			name:     "tar.gz archive",
			input:    "azd-ext-ai-linux-amd64.tar.gz",
			expected: "azd-ext-ai-linux-amd64",
		},
		{
			name:     ".zip archive",
			input:    "azd-ext-ai-windows-amd64.zip",
			expected: "azd-ext-ai-windows-amd64",
		},
		{
			name:     "File with no extension",
			input:    "binary",
			expected: "binary",
		},
		{
			name:     "File with multiple dots but not .tar.gz",
			input:    "my.config.json",
			expected: "my.config",
		},
		{
			name:     "Full path with .tar.gz",
			input:    "/path/to/file.tar.gz",
			expected: "file",
		},
		{
			name:     "Full path with single extension",
			input:    "/path/to/file.txt",
			expected: "file",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "Just an extension",
			input:    ".gitignore",
			expected: "",
		},
	}

	if runtime.GOOS == "windows" {
		tests = append(tests, struct {
			name     string
			input    string
			expected string
		}{
			name:     "Windows path with extension",
			input:    "C:\\path\\to\\file.exe",
			expected: "file",
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetFileNameWithoutExt(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestAzdConfigDir(t *testing.T) {
	// Test with AZD_CONFIG_DIR set
	tempDir := t.TempDir()
	originalAzdConfigDir := os.Getenv("AZD_CONFIG_DIR")
	os.Setenv("AZD_CONFIG_DIR", tempDir)
	defer os.Setenv("AZD_CONFIG_DIR", originalAzdConfigDir)

	configDir, err := AzdConfigDir()
	require.NoError(t, err, "Should be able to get AZD config dir")
	require.Equal(t, tempDir, configDir, "Should return set AZD_CONFIG_DIR")

	// Test without AZD_CONFIG_DIR set
	os.Unsetenv("AZD_CONFIG_DIR")

	configDir, err = AzdConfigDir()
	require.NoError(t, err, "Should be able to get AZD config dir even without env var")
	require.Contains(t, configDir, ".azd", "Should contain .azd directory")
}

func TestCreateLocalRegistryFile(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Set AZD_CONFIG_DIR to our temp directory
	originalAzdConfigDir := os.Getenv("AZD_CONFIG_DIR")
	os.Setenv("AZD_CONFIG_DIR", tempDir)
	defer os.Setenv("AZD_CONFIG_DIR", originalAzdConfigDir)

	// Verify the registry file doesn't exist initially
	registryPath := filepath.Join(tempDir, "registry.json")
	_, err := os.Stat(registryPath)
	require.True(t, os.IsNotExist(err), "Registry file should not exist initially")

	// Test creating the registry file (just the file creation part, not the full CreateLocalRegistry)
	emptyRegistry := map[string]any{
		"registry": []any{},
	}

	registryJson, err := json.MarshalIndent(emptyRegistry, "", "  ")
	require.NoError(t, err, "Should be able to marshal empty registry")

	err = os.WriteFile(registryPath, registryJson, PermissionFile)
	require.NoError(t, err, "Should be able to write registry file")

	// Verify the file was created
	_, err = os.Stat(registryPath)
	require.NoError(t, err, "Registry file should exist after creation")

	// Verify the content is correct
	content, err := os.ReadFile(registryPath)
	require.NoError(t, err, "Should be able to read registry file")
	require.Contains(t, string(content), "\"registry\"", "Registry file should contain registry field")
	require.Contains(t, string(content), "[]", "Registry should be empty array")
}
