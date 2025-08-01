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
