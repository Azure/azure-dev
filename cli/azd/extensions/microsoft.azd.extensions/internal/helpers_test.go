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

func TestGlobArtifacts(t *testing.T) {
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
		pattern  string
		expected []string
		wantErr  bool
	}{
		{
			name:     "Concrete file path - zip",
			pattern:  zipFile,
			expected: []string{zipFile},
			wantErr:  false,
		},
		{
			name:     "Concrete file path - tar.gz",
			pattern:  tarGzFile,
			expected: []string{tarGzFile},
			wantErr:  false,
		},
		{
			name:     "Concrete file path - non-existent",
			pattern:  filepath.Join(tempDir, "nonexistent.zip"),
			expected: []string{},
			wantErr:  false,
		},
		{
			name:     "Glob pattern",
			pattern:  filepath.Join(tempDir, "*"),
			expected: []string{zipFile, tarGzFile},
			wantErr:  false,
		},
		{
			name:    "Glob pattern with extension",
			pattern: filepath.Join(tempDir, "*.zip"),
			// Both files should be found since GlobArtifacts looks for both .zip and .tar.gz
			expected: []string{zipFile, tarGzFile},
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := GlobArtifacts(tt.pattern)
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
