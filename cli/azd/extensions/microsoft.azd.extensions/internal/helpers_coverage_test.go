// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToPascalCase(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "SingleWord",
			input:    "hello",
			expected: "Hello",
		},
		{
			name:     "DotSeparated",
			input:    "azure.ai.models",
			expected: "Azure.Ai.Models",
		},
		{
			name:     "AlreadyPascal",
			input:    "Hello.World",
			expected: "Hello.World",
		},
		{
			name:     "EmptyString",
			input:    "",
			expected: "",
		},
		{
			name:     "SingleChar",
			input:    "a",
			expected: "A",
		},
		{
			name:     "SingleDot",
			input:    "a.b",
			expected: "A.B",
		},
		{
			name:     "EmptyParts",
			input:    "a..b",
			expected: "A..B",
		},
		{
			name:     "TrailingDot",
			input:    "hello.",
			expected: "Hello.",
		},
		{
			name:     "LeadingDot",
			input:    ".hello",
			expected: ".Hello",
		},
		{
			name:     "NoDots",
			input:    "helloworld",
			expected: "Helloworld",
		},
		{
			name:     "Unicode",
			input:    "über.straße",
			expected: "Über.Straße",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ToPascalCase(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestComputeChecksum(t *testing.T) {
	t.Run("ValidFile", func(t *testing.T) {
		tempDir := t.TempDir()
		filePath := filepath.Join(tempDir, "test.txt")
		require.NoError(t, os.WriteFile(
			filePath, []byte("hello world"), 0600,
		))

		checksum, err := ComputeChecksum(filePath)
		require.NoError(t, err)
		// SHA-256 of "hello world"
		require.Equal(t,
			"b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			checksum,
		)
	})

	t.Run("EmptyFile", func(t *testing.T) {
		tempDir := t.TempDir()
		filePath := filepath.Join(tempDir, "empty.txt")
		require.NoError(t, os.WriteFile(
			filePath, []byte{}, 0600,
		))

		checksum, err := ComputeChecksum(filePath)
		require.NoError(t, err)
		// SHA-256 of empty input
		require.Equal(t,
			"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			checksum,
		)
	})

	t.Run("NonExistentFile", func(t *testing.T) {
		_, err := ComputeChecksum("/nonexistent/file.txt")
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to open file")
	})
}

func TestCopyFile(t *testing.T) {
	t.Run("SuccessfulCopy", func(t *testing.T) {
		tempDir := t.TempDir()
		srcPath := filepath.Join(tempDir, "source.txt")
		dstPath := filepath.Join(tempDir, "dest.txt")

		content := []byte("test file content")
		require.NoError(t, os.WriteFile(srcPath, content, 0600))

		err := CopyFile(srcPath, dstPath)
		require.NoError(t, err)

		copied, err := os.ReadFile(dstPath)
		require.NoError(t, err)
		require.Equal(t, content, copied)
	})

	t.Run("SourceNotFound", func(t *testing.T) {
		tempDir := t.TempDir()
		err := CopyFile(
			filepath.Join(tempDir, "nonexistent.txt"),
			filepath.Join(tempDir, "dest.txt"),
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to open source file")
	})

	t.Run("OverwriteExisting", func(t *testing.T) {
		tempDir := t.TempDir()
		srcPath := filepath.Join(tempDir, "source.txt")
		dstPath := filepath.Join(tempDir, "dest.txt")

		require.NoError(t, os.WriteFile(
			srcPath, []byte("new content"), 0600,
		))
		require.NoError(t, os.WriteFile(
			dstPath, []byte("old content"), 0600,
		))

		err := CopyFile(srcPath, dstPath)
		require.NoError(t, err)

		result, err := os.ReadFile(dstPath)
		require.NoError(t, err)
		require.Equal(t, "new content", string(result))
	})

	t.Run("LargeFile", func(t *testing.T) {
		tempDir := t.TempDir()
		srcPath := filepath.Join(tempDir, "large.bin")
		dstPath := filepath.Join(tempDir, "large_copy.bin")

		data := make([]byte, 1024*1024) // 1 MB
		for i := range data {
			data[i] = byte(i % 256)
		}
		require.NoError(t, os.WriteFile(srcPath, data, 0600))

		err := CopyFile(srcPath, dstPath)
		require.NoError(t, err)

		copied, err := os.ReadFile(dstPath)
		require.NoError(t, err)
		require.Equal(t, data, copied)
	})
}

func TestZipSource(t *testing.T) {
	// ZipSource has a known file-handle leak on Windows (os.Open without
	// Close), so t.TempDir() cleanup fails. Use os.MkdirTemp + manual
	// cleanup that tolerates locked files.

	t.Run("CreateZipWithMultipleFiles", func(t *testing.T) {
		tempDir, err := os.MkdirTemp("", "ziptest-*")
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.RemoveAll(tempDir) })

		file1 := filepath.Join(tempDir, "file1.txt")
		file2 := filepath.Join(tempDir, "file2.txt")
		targetZip := filepath.Join(tempDir, "output.zip")

		require.NoError(t, os.WriteFile(
			file1, []byte("content one"), 0600,
		))
		require.NoError(t, os.WriteFile(
			file2, []byte("content two"), 0600,
		))

		err = ZipSource([]string{file1, file2}, targetZip)
		require.NoError(t, err)

		info, err := os.Stat(targetZip)
		require.NoError(t, err)
		require.Greater(t, info.Size(), int64(0))

		reader, err := zip.OpenReader(targetZip)
		require.NoError(t, err)

		require.Len(t, reader.File, 2)
		require.Equal(t, "file1.txt", reader.File[0].Name)
		require.Equal(t, "file2.txt", reader.File[1].Name)

		rc, err := reader.File[0].Open()
		require.NoError(t, err)
		data, err := io.ReadAll(rc)
		require.NoError(t, err)
		rc.Close()
		require.Equal(t, "content one", string(data))

		rc, err = reader.File[1].Open()
		require.NoError(t, err)
		data, err = io.ReadAll(rc)
		require.NoError(t, err)
		rc.Close()
		require.Equal(t, "content two", string(data))

		reader.Close()
	})

	t.Run("NonExistentSourceFile", func(t *testing.T) {
		tempDir := t.TempDir()
		err := ZipSource(
			[]string{filepath.Join(tempDir, "missing.txt")},
			filepath.Join(tempDir, "out.zip"),
		)
		require.Error(t, err)
	})

	t.Run("SingleFile", func(t *testing.T) {
		tempDir, err := os.MkdirTemp("", "ziptest-*")
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.RemoveAll(tempDir) })

		srcFile := filepath.Join(tempDir, "single.txt")
		targetZip := filepath.Join(tempDir, "single.zip")

		require.NoError(t, os.WriteFile(
			srcFile, []byte("only file"), 0600,
		))

		err = ZipSource([]string{srcFile}, targetZip)
		require.NoError(t, err)

		reader, err := zip.OpenReader(targetZip)
		require.NoError(t, err)

		require.Len(t, reader.File, 1)
		require.Equal(t, "single.txt", reader.File[0].Name)

		reader.Close()
	})
}

func TestDownloadAssetToTemp_LocalFile(t *testing.T) {
	tempDir := t.TempDir()
	localFile := filepath.Join(tempDir, "asset.bin")
	content := []byte("local asset data")
	require.NoError(t, os.WriteFile(localFile, content, 0600))

	result, err := DownloadAssetToTemp(localFile, "asset.bin")
	require.NoError(t, err)
	defer os.Remove(result)

	data, err := os.ReadFile(result)
	require.NoError(t, err)
	require.Equal(t, content, data)
}

func TestDownloadAssetToTemp_NonExistentLocal(t *testing.T) {
	_, err := DownloadAssetToTemp(
		"/nonexistent/path/asset.bin", "asset.bin",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to open local file")
}

func TestToPtr(t *testing.T) {
	t.Run("Int", func(t *testing.T) {
		p := ToPtr(42)
		require.NotNil(t, p)
		require.Equal(t, 42, *p)
	})

	t.Run("String", func(t *testing.T) {
		p := ToPtr("hello")
		require.NotNil(t, p)
		require.Equal(t, "hello", *p)
	})

	t.Run("Bool", func(t *testing.T) {
		p := ToPtr(true)
		require.NotNil(t, p)
		require.True(t, *p)
	})

	t.Run("ZeroValue", func(t *testing.T) {
		p := ToPtr(0)
		require.NotNil(t, p)
		require.Equal(t, 0, *p)
	})
}
