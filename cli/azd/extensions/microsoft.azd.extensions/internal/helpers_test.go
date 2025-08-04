// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
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

func TestFindFiles(t *testing.T) {
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
		name     string
		patterns []string
		expected []string
		wantErr  bool
	}{
		{
			name:     "Concrete file path - zip",
			patterns: []string{zipFile},
			expected: []string{zipFile},
			wantErr:  false,
		},
		{
			name:     "Concrete file path - tar.gz",
			patterns: []string{tarGzFile},
			expected: []string{tarGzFile},
			wantErr:  false,
		},
		{
			name:     "Concrete file path - non-existent",
			patterns: []string{filepath.Join(tempDir, "nonexistent.zip")},
			expected: []string{},
			wantErr:  false,
		},
		{
			name:     "Multiple patterns",
			patterns: []string{filepath.Join(tempDir, "*.zip"), filepath.Join(tempDir, "*.tar.gz")},
			expected: []string{zipFile, tarGzFile},
			wantErr:  false,
		},
		{
			name:     "Single glob pattern",
			patterns: []string{filepath.Join(tempDir, "*.zip")},
			expected: []string{zipFile},
			wantErr:  false,
		},
		{
			name:     "Mixed concrete and pattern",
			patterns: []string{zipFile, filepath.Join(tempDir, "*.tar.gz")},
			expected: []string{zipFile, tarGzFile},
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := FindFiles(tt.patterns)
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

func TestArtifactPatterns(t *testing.T) {
	tests := []struct {
		name           string
		artifactsFlag  string
		extensionId    string
		version        string
		expectedCount  int
		expectedSuffix []string
	}{
		{
			name:           "Empty artifacts flag uses default patterns",
			artifactsFlag:  "",
			extensionId:    "test.extension",
			version:        "1.0.0",
			expectedCount:  2,
			expectedSuffix: []string{"*.zip", "*.tar.gz"},
		},
		{
			name:           "Explicit artifacts flag returns single pattern",
			artifactsFlag:  "./out/test.zip",
			extensionId:    "test.extension",
			version:        "1.0.0",
			expectedCount:  1,
			expectedSuffix: []string{"./out/test.zip"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patterns, err := ArtifactPatterns(tt.artifactsFlag, tt.extensionId, tt.version)
			require.NoError(t, err)
			require.Len(t, patterns, tt.expectedCount)

			if tt.artifactsFlag == "" {
				// For default patterns, check they end with the expected suffixes
				for i, suffix := range tt.expectedSuffix {
					require.Contains(t, patterns[i], suffix)
				}
			} else {
				// For explicit pattern, should match exactly
				require.Equal(t, tt.expectedSuffix[0], patterns[0])
			}
		})
	}
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
			name:     "Windows path with extension",
			input:    "C:\\path\\to\\file.exe",
			expected: "C:\\path\\to\\file", // On Unix systems, backslashes are not treated as path separators
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetFileNameWithoutExt(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}
