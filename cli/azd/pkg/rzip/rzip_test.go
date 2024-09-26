package rzip_test

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/rzip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateFromDirectory(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	tempDir := t.TempDir()

	// ASCII representation of the filesystem structure inside tempDir
	/*
		tempDir/
		├── file1.txt
		├── subdir/
		│   ├── file2.txt
		│   └── file3.txt
		├── symlink_to_file1.txt -> file1.txt
		├── symlink_to_subdir -> subdir/
		├── symlink_to_symlink_to_file1.txt -> symlink_to_file1.txt
		└── symlink_to_symlink_to_subdir -> symlink_to_subdir/
	*/

	files := map[string]string{
		"file1.txt":        "Content of file1",
		"subdir/file2.txt": "Content of file2",
		"subdir/file3.txt": "Content of file3",
	}

	for path, content := range files {
		fullPath := filepath.Join(tempDir, path)
		err := os.MkdirAll(filepath.Dir(fullPath), 0755)
		require.NoError(err)
		err = os.WriteFile(fullPath, []byte(content), 0644)
		require.NoError(err)
	}

	// Create symlinks
	err := os.Symlink(filepath.Join(tempDir, "file1.txt"), filepath.Join(tempDir, "symlink_to_file1.txt"))
	require.NoError(err)
	err = os.Symlink(filepath.Join(tempDir, "symlink_to_file1.txt"), filepath.Join(tempDir, "symlink_to_symlink_to_file1.txt"))
	require.NoError(err)
	err = os.Symlink(filepath.Join(tempDir, "subdir"), filepath.Join(tempDir, "symlink_to_subdir"))
	require.NoError(err)
	err = os.Symlink(filepath.Join(tempDir, "symlink_to_subdir"), filepath.Join(tempDir, "symlink_to_symlink_to_subdir"))
	require.NoError(err)

	// Create zip file
	zipFile, err := os.CreateTemp("", "test_archive_*.zip")
	require.NoError(err, "failed to create temp zip file")
	defer os.Remove(zipFile.Name())
	defer zipFile.Close()

	// zip the directory
	err = rzip.CreateFromDirectory(tempDir, zipFile)
	require.NoError(err)

	// Reopen the zip file for reading
	_, err = zipFile.Seek(0, 0)
	require.NoError(err, "failed to seek to start of zip file")
	zipInfo, err := zipFile.Stat()
	require.NoError(err, "failed to get zip file info")
	zipReader, err := zip.NewReader(zipFile, zipInfo.Size())
	require.NoError(err, "failed to open zip for reading")

	// Check zip contents
	expectedFiles := map[string]string{
		"file1.txt":                              "Content of file1",
		"subdir/file2.txt":                       "Content of file2",
		"subdir/file3.txt":                       "Content of file3",
		"symlink_to_file1.txt":                   "Content of file1",
		"symlink_to_symlink_to_file1.txt":        "Content of file1",
		"symlink_to_subdir/file2.txt":            "Content of file2",
		"symlink_to_subdir/file3.txt":            "Content of file3",
		"symlink_to_symlink_to_subdir/file2.txt": "Content of file2",
		"symlink_to_symlink_to_subdir/file3.txt": "Content of file3",
	}

	for _, zipFile := range zipReader.File {
		expectedContent, exists := expectedFiles[zipFile.Name]
		assert.True(exists, "unexpected file in zip: %s", zipFile.Name)

		rc, err := zipFile.Open()
		assert.NoError(err, "failed to open file in zip: %s", zipFile.Name)
		content, err := io.ReadAll(rc)
		rc.Close()
		assert.NoError(err, "failed to read file in zip: %s", zipFile.Name)

		assert.Equal(expectedContent, string(content), "incorrect content for %s", zipFile.Name)

		delete(expectedFiles, zipFile.Name)
	}

	assert.Empty(expectedFiles, "expected files not found in zip: %v", expectedFiles)
}
