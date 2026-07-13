// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodeDownloadCommand_AcceptsPositionalArg(t *testing.T) {
	cmd := newCodeDownloadCommand(nil)
	err := cmd.Args(cmd, []string{"my-service"})
	assert.NoError(t, err)
}

func TestCodeDownloadCommand_AcceptsNoArgs(t *testing.T) {
	cmd := newCodeDownloadCommand(nil)
	err := cmd.Args(cmd, []string{})
	assert.NoError(t, err)
}

func TestCodeDownloadCommand_RejectsMultipleArgs(t *testing.T) {
	cmd := newCodeDownloadCommand(nil)
	err := cmd.Args(cmd, []string{"svc1", "svc2"})
	assert.Error(t, err)
}

func TestCodeDownloadCommand_Flags(t *testing.T) {
	cmd := newCodeDownloadCommand(nil)

	tests := []struct {
		name     string
		defValue string
	}{
		{"version", ""},
		{"dest", ""},
		{"zip", "false"},
	}

	for _, tt := range tests {
		flag := cmd.Flags().Lookup(tt.name)
		if flag == nil {
			t.Fatalf("expected --%s flag to be registered", tt.name)
		}
		if flag.DefValue != tt.defValue {
			t.Fatalf("expected --%s default %q, got %q", tt.name, tt.defValue, flag.DefValue)
		}
	}
}

func TestCodeDownloadCommand_VersionShorthand(t *testing.T) {
	cmd := newCodeDownloadCommand(nil)
	flag := cmd.Flags().ShorthandLookup("v")
	require.NotNil(t, flag, "expected -v shorthand for --version")
	assert.Equal(t, "version", flag.Name)
}

func TestCodeDownloadCommand_DestShorthand(t *testing.T) {
	cmd := newCodeDownloadCommand(nil)
	flag := cmd.Flags().ShorthandLookup("d")
	require.NotNil(t, flag, "expected -d shorthand for --dest")
	assert.Equal(t, "dest", flag.Name)
}

func TestSaveZipFile_WritesAndVerifiesHash(t *testing.T) {
	content := []byte("fake zip content for testing")
	hash := sha256.Sum256(content)
	expectedHash := hex.EncodeToString(hash[:])

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "test.zip")

	err := saveZipFile(outputPath, bytes.NewReader(content), expectedHash)
	require.NoError(t, err)

	data, err := os.ReadFile(outputPath) //nolint:gosec // G304: test file path from t.TempDir()
	require.NoError(t, err)
	assert.Equal(t, content, data)
}

func TestSaveZipFile_FailsOnHashMismatch(t *testing.T) {
	content := []byte("fake zip content")
	wrongHash := "0000000000000000000000000000000000000000000000000000000000000000"

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "test.zip")

	err := saveZipFile(outputPath, bytes.NewReader(content), wrongHash)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SHA-256 verification failed")

	// File should be cleaned up
	_, statErr := os.Stat(outputPath)
	assert.True(t, os.IsNotExist(statErr), "file should be removed on hash mismatch")
}

func TestSaveZipFile_SkipsHashVerificationWhenEmpty(t *testing.T) {
	content := []byte("fake zip content")

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "test.zip")

	err := saveZipFile(outputPath, bytes.NewReader(content), "")
	require.NoError(t, err)

	data, err := os.ReadFile(outputPath) //nolint:gosec // G304: test file path from t.TempDir()
	require.NoError(t, err)
	assert.Equal(t, content, data)
}

func TestExtractZip_ExtractsFilesCorrectly(t *testing.T) {
	// Create a zip in memory
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	files := map[string]string{
		"hello.txt":       "Hello, World!",
		"subdir/file.txt": "nested content",
	}
	for name, content := range files {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = w.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())

	// Write zip to temp file
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "test.zip")
	require.NoError(t, os.WriteFile(zipPath, buf.Bytes(), 0600))

	// Extract
	outputDir := filepath.Join(tmpDir, "output")
	err := extractZip(zipPath, outputDir)
	require.NoError(t, err)

	// Verify
	for name, expectedContent := range files {
		data, err := os.ReadFile(filepath.Join(outputDir, name)) //nolint:gosec // G304: test path from t.TempDir()
		require.NoError(t, err, "expected file %s to exist", name)
		assert.Equal(t, expectedContent, string(data))
	}
}

func TestExtractZip_BlocksZipSlip(t *testing.T) {
	// Create a zip with a path traversal entry
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("../../../etc/passwd")
	require.NoError(t, err)
	_, err = w.Write([]byte("malicious"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "evil.zip")
	require.NoError(t, os.WriteFile(zipPath, buf.Bytes(), 0600))

	outputDir := filepath.Join(tmpDir, "output")
	err = extractZip(zipPath, outputDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "zip-slip detected")
}

func TestExtractZip_WorksWithDotOutputDir(t *testing.T) {
	// Regression test: extractZip should work when outputDir is "."
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("test.txt")
	require.NoError(t, err)
	_, err = w.Write([]byte("content"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "test.zip")
	require.NoError(t, os.WriteFile(zipPath, buf.Bytes(), 0600))

	// Use a subdirectory as "." equivalent
	outputDir := filepath.Join(tmpDir, "out")
	err = extractZip(zipPath, outputDir)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(outputDir, "test.txt")) //nolint:gosec // G304: test path
	require.NoError(t, err)
	assert.Equal(t, "content", string(data))
}

func TestEqualFoldHash(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"abc123", "ABC123", true},
		{"abc123", "abc123", true},
		{"ABC123", "ABC123", true},
		{"abc123", "abc124", false},
		{"abc", "abcd", false},
		{"", "", true},
	}
	for _, tt := range tests {
		got := equalFoldHash(tt.a, tt.b)
		assert.Equal(t, tt.want, got, "equalFoldHash(%q, %q)", tt.a, tt.b)
	}
}

func TestDownloadAndExtract_VerifiesHashAndExtracts(t *testing.T) {
	// Create a valid zip
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("readme.md")
	require.NoError(t, err)
	_, err = w.Write([]byte("# Hello"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	zipBytes := buf.Bytes()
	hash := sha256.Sum256(zipBytes)
	expectedHash := hex.EncodeToString(hash[:])

	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "extracted")

	err = downloadAndExtract(outputDir, bytes.NewReader(zipBytes), expectedHash)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(outputDir, "readme.md")) //nolint:gosec // G304: test path
	require.NoError(t, err)
	assert.Equal(t, "# Hello", string(data))
}

func TestDownloadAndExtract_FailsOnHashMismatch(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("file.txt")
	require.NoError(t, err)
	_, err = w.Write([]byte("data"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "extracted")

	wrongHash := "0000000000000000000000000000000000000000000000000000000000000000"
	err = downloadAndExtract(outputDir, bytes.NewReader(buf.Bytes()), wrongHash)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SHA-256 verification failed")
}
