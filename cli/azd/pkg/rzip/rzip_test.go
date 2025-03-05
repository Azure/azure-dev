// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package rzip_test

import (
	"archive/zip"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/rzip"
	"github.com/stretchr/testify/require"
)

func TestCreateFromDirectory(t *testing.T) {
	require := require.New(t)
	tempDir := t.TempDir()

	// ASCII representation of the filesystem structure inside tempDir
	/*
		tempDir/
		├── file1.txt
		├── a/b/c/d/deep.txt
		├── empty/
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
		"a/b/c/d/deep.txt": "Content of deep",
	}

	for path, content := range files {
		fullPath := filepath.Join(tempDir, path)
		err := os.MkdirAll(filepath.Dir(fullPath), 0755)
		require.NoError(err)
		err = os.WriteFile(fullPath, []byte(content), 0600)
		require.NoError(err)
	}

	// Create empty directory
	err := os.Mkdir(filepath.Join(tempDir, "empty"), 0755)
	require.NoError(err)

	// Create symlinks -- both relative and absolute links
	err = os.Symlink(filepath.Join(".", "file1.txt"), filepath.Join(tempDir, "symlink_to_file1.txt"))
	require.NoError(err)
	//nolint:lll
	err = os.Symlink(
		filepath.Join(tempDir, "symlink_to_file1.txt"),
		filepath.Join(tempDir, "symlink_to_symlink_to_file1.txt"),
	)
	require.NoError(err)
	err = os.Symlink(filepath.Join(".", "subdir"), filepath.Join(tempDir, "symlink_to_subdir"))
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
		"a/b/c/d/deep.txt":                       "Content of deep",
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
		if !exists {
			t.Errorf("unexpected file in zip: %s", zipFile.Name)
			continue
		}

		rc, err := zipFile.Open()
		if err != nil {
			panic(err)
		}

		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			panic(err)
		}

		if expectedContent != string(content) {
			t.Errorf("incorrect content for %s", zipFile.Name)
		}

		delete(expectedFiles, zipFile.Name)
	}

	if len(expectedFiles) > 0 {
		t.Errorf("missing files:\n%v", formatFiles(expectedFiles))
	}
}

func TestCreateFromDirectory_SymlinkRecursive(t *testing.T) {
	tmp := t.TempDir()

	err := os.Mkdir(filepath.Join(tmp, "dir"), 0755)
	require.NoError(t, err)

	err = os.Symlink("../", filepath.Join(tmp, "dir", "dir_symlink"))
	require.NoError(t, err)

	// Create zip file
	zipFile, err := os.CreateTemp("", "test_archive_*.zip")
	require.NoError(t, err, "failed to create temp zip file")
	defer os.Remove(zipFile.Name())
	defer zipFile.Close()

	// zip the directory
	err = rzip.CreateFromDirectory(tmp, zipFile)
	require.NoError(t, err)
}

func formatFiles(files map[string]string) string {
	var sb strings.Builder
	keys := slices.Collect(maps.Keys(files))
	slices.Sort(keys)
	for _, path := range keys {
		sb.WriteString(fmt.Sprintf("- %s\n", path))
	}
	return sb.String()
}
