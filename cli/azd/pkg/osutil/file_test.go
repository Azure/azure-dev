// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package osutil_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/stretchr/testify/assert"
)

func TestDirExists(t *testing.T) {
	// Create a temporary directory
	tempDir := t.TempDir()

	// Test when directory exists
	assert.True(t, osutil.DirExists(tempDir))

	// Test when directory does not exist
	nonExistentDir := filepath.Join(tempDir, "nonexistent")
	assert.False(t, osutil.DirExists(nonExistentDir))
}

func TestFileExists(t *testing.T) {
	// Create a temporary directory
	tempDir := t.TempDir()

	// Create a temporary file
	tempFile := filepath.Join(tempDir, "tempfile.txt")
	file, err := os.Create(tempFile)
	assert.NoError(t, err)
	file.Close()

	// Test when file exists and is a regular file
	assert.True(t, osutil.FileExists(tempFile))

	// Test when file does not exist
	nonExistentFile := filepath.Join(tempDir, "nonexistent.txt")
	assert.False(t, osutil.FileExists(nonExistentFile))

	// Test when path is a directory
	assert.False(t, osutil.FileExists(tempDir))
}

func TestIsDirEmpty(t *testing.T) {
	// Create a temporary directory
	tempDir := t.TempDir()

	// Test when directory is empty
	isEmpty, err := osutil.IsDirEmpty(tempDir)
	assert.NoError(t, err)
	assert.True(t, isEmpty)

	// Create a temporary file in the directory
	tempFile := filepath.Join(tempDir, "tempfile.txt")
	file, err := os.Create(tempFile)
	assert.NoError(t, err)
	file.Close()

	// Test when directory is not empty
	isEmpty, err = osutil.IsDirEmpty(tempDir)
	assert.NoError(t, err)
	assert.False(t, isEmpty)

	// Test when directory does not exist (treating missing as error)
	nonExistentDir := filepath.Join(tempDir, "nonexistent")
	isEmpty, err = osutil.IsDirEmpty(nonExistentDir)
	assert.Error(t, err)
	assert.False(t, isEmpty)

	// Test when directory does not exist (treating missing as empty)
	isEmpty, err = osutil.IsDirEmpty(nonExistentDir, true)
	assert.NoError(t, err)
	assert.True(t, isEmpty)
}
