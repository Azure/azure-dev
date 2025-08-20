// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package io

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteFileTool_Name(t *testing.T) {
	tool := WriteFileTool{}
	assert.Equal(t, "write_file", tool.Name())
}

func TestWriteFileTool_Description(t *testing.T) {
	tool := WriteFileTool{}
	desc := tool.Description()
	assert.Contains(t, desc, "Comprehensive file writing tool")
	assert.Contains(t, desc, "partial")
	assert.Contains(t, desc, "startLine")
	assert.Contains(t, desc, "endLine")
}

func TestWriteFileTool_Call_EmptyInput(t *testing.T) {
	tool := WriteFileTool{}
	result, err := tool.Call(context.Background(), "")

	assert.NoError(t, err)
	assert.Contains(t, result, "error")
	assert.Contains(t, result, "No input provided")
}

func TestWriteFileTool_Call_InvalidJSON(t *testing.T) {
	tool := WriteFileTool{}
	result, err := tool.Call(context.Background(), "invalid json")

	assert.NoError(t, err)
	assert.Contains(t, result, "error")
	assert.Contains(t, result, "Invalid JSON input: Input does not appear to be valid JSON object")
}

func TestWriteFileTool_Call_MalformedJSON(t *testing.T) {
	tool := WriteFileTool{}
	// Test with JSON that has parse errors
	result, err := tool.Call(context.Background(), `{"path": "test.txt", "content": "unclosed string}`)

	assert.NoError(t, err)
	assert.Contains(t, result, "error")
	assert.Contains(t, result, "Invalid JSON input. Error:")
	assert.Contains(t, result, "Input (first 200 chars):")
}

func TestWriteFileTool_Call_MissingFilename(t *testing.T) {
	tool := WriteFileTool{}
	input := `{"content": "test content"}`
	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)
	assert.Contains(t, result, "error")
	assert.Contains(t, result, "filename cannot be empty")
}

func TestWriteFileTool_FullFileWrite(t *testing.T) {
	// Create temp directory
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.txt")

	tool := WriteFileTool{}
	input := `{"path": "` + strings.ReplaceAll(testFile, "\\", "\\\\") + `", "content": "Hello, World!"}`

	result, err := tool.Call(context.Background(), input)
	assert.NoError(t, err)

	// Verify response
	var response WriteFileResponse
	err = json.Unmarshal([]byte(result), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.Equal(t, "Wrote", response.Operation)
	assert.Equal(t, testFile, response.Path)
	assert.Equal(t, 13, response.BytesWritten) // "Hello, World!" length
	assert.False(t, response.IsPartial)
	assert.Nil(t, response.LineInfo)
	assert.Greater(t, response.FileInfo.Size, int64(0))

	// Verify file content
	content, err := os.ReadFile(testFile)
	assert.NoError(t, err)
	assert.Equal(t, "Hello, World!", string(content))
}

func TestWriteFileTool_AppendMode(t *testing.T) {
	// Create temp directory and initial file
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.txt")

	// Create initial file
	err := os.WriteFile(testFile, []byte("Initial content"), 0600)
	require.NoError(t, err)

	tool := WriteFileTool{}
	input := `{"path": "` + strings.ReplaceAll(
		testFile,
		"\\",
		"\\\\",
	) + `", "content": "\nAppended content", "mode": "append"}`

	result, err := tool.Call(context.Background(), input)
	assert.NoError(t, err)

	// Verify response
	var response WriteFileResponse
	err = json.Unmarshal([]byte(result), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.Equal(t, "Appended to", response.Operation)
	assert.False(t, response.IsPartial)

	// Verify file content
	content, err := os.ReadFile(testFile)
	assert.NoError(t, err)
	assert.Equal(t, "Initial content\nAppended content", string(content))
}

func TestWriteFileTool_CreateMode_Success(t *testing.T) {
	// Create temp directory
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "new-file.txt")

	tool := WriteFileTool{}
	input := `{"path": "` + strings.ReplaceAll(
		testFile,
		"\\",
		"\\\\",
	) + `", "content": "New file content", "mode": "create"}`

	result, err := tool.Call(context.Background(), input)
	assert.NoError(t, err)

	// Verify response
	var response WriteFileResponse
	err = json.Unmarshal([]byte(result), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.Equal(t, "Created", response.Operation)

	// Verify file content
	content, err := os.ReadFile(testFile)
	assert.NoError(t, err)
	assert.Equal(t, "New file content", string(content))
}

func TestWriteFileTool_CreateMode_FileExists(t *testing.T) {
	// Create temp directory and existing file
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "existing.txt")

	err := os.WriteFile(testFile, []byte("Existing content"), 0600)
	require.NoError(t, err)

	tool := WriteFileTool{}
	input := `{"path": "` + strings.ReplaceAll(testFile, "\\", "\\\\") + `", "content": "New content", "mode": "create"}`

	result, err := tool.Call(context.Background(), input)
	assert.NoError(t, err)

	// Should return error
	assert.Contains(t, result, "error")
	assert.Contains(t, result, "already exists")

	// Verify original content unchanged
	content, err := os.ReadFile(testFile)
	assert.NoError(t, err)
	assert.Equal(t, "Existing content", string(content))
}

func TestWriteFileTool_PartialWrite_Basic(t *testing.T) {
	// Create temp directory and initial file
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.txt")

	// Create initial file with multiple lines
	initialContent := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"
	err := os.WriteFile(testFile, []byte(initialContent), 0600)
	require.NoError(t, err)

	tool := WriteFileTool{}
	input := `{"path": "` + strings.ReplaceAll(
		testFile,
		"\\",
		"\\\\",
	) + `", "content": "Modified Line 2\nModified Line 3", "startLine": 2, "endLine": 3}`

	result, err := tool.Call(context.Background(), input)
	assert.NoError(t, err)

	// Verify response
	var response WriteFileResponse
	err = json.Unmarshal([]byte(result), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.Equal(t, "Wrote (partial)", response.Operation)
	assert.True(t, response.IsPartial)
	assert.NotNil(t, response.LineInfo)
	assert.Equal(t, 2, response.LineInfo.StartLine)
	assert.Equal(t, 3, response.LineInfo.EndLine)
	assert.Equal(t, 2, response.LineInfo.LinesChanged)

	// Verify file content
	content, err := os.ReadFile(testFile)
	assert.NoError(t, err)
	expectedContent := "Line 1\nModified Line 2\nModified Line 3\nLine 4\nLine 5"
	assert.Equal(t, expectedContent, string(content))
}

func TestWriteFileTool_PartialWrite_SingleLine(t *testing.T) {
	// Create temp directory and initial file
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.txt")

	// Create initial file
	initialContent := "Line 1\nLine 2\nLine 3"
	err := os.WriteFile(testFile, []byte(initialContent), 0600)
	require.NoError(t, err)

	tool := WriteFileTool{}
	input := `{"path": "` + strings.ReplaceAll(
		testFile,
		"\\",
		"\\\\",
	) + `", "content": "Replaced Line 2", "startLine": 2, "endLine": 2}`

	result, err := tool.Call(context.Background(), input)
	assert.NoError(t, err)

	// Verify response
	var response WriteFileResponse
	err = json.Unmarshal([]byte(result), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.True(t, response.IsPartial)
	assert.Equal(t, 2, response.LineInfo.StartLine)
	assert.Equal(t, 2, response.LineInfo.EndLine)
	assert.Equal(t, 1, response.LineInfo.LinesChanged)

	// Verify file content
	content, err := os.ReadFile(testFile)
	assert.NoError(t, err)
	expectedContent := "Line 1\nReplaced Line 2\nLine 3"
	assert.Equal(t, expectedContent, string(content))
}

func TestWriteFileTool_PartialWrite_SingleLineToMultipleLines(t *testing.T) {
	// Create temp directory and initial file
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.txt")

	// Create initial file
	initialContent := "Line 1\nLine 2\nLine 3\nLine 4"
	err := os.WriteFile(testFile, []byte(initialContent), 0600)
	require.NoError(t, err)

	tool := WriteFileTool{}
	// Replace single line 2 with multiple lines
	input := `{"path": "` + strings.ReplaceAll(
		testFile,
		"\\",
		"\\\\",
	) + `", "content": "New Line 2a\nNew Line 2b\nNew Line 2c", "startLine": 2, "endLine": 2}`

	result, err := tool.Call(context.Background(), input)
	assert.NoError(t, err)

	// Verify response
	var response WriteFileResponse
	err = json.Unmarshal([]byte(result), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.Equal(t, "Wrote (partial)", response.Operation)
	assert.True(t, response.IsPartial)
	assert.NotNil(t, response.LineInfo)
	assert.Equal(t, 2, response.LineInfo.StartLine)
	assert.Equal(t, 2, response.LineInfo.EndLine)
	assert.Equal(t, 3, response.LineInfo.LinesChanged) // 3 new lines replaced 1 line

	// Verify file content - single line 2 should be replaced with 3 lines
	content, err := os.ReadFile(testFile)
	assert.NoError(t, err)
	expectedContent := "Line 1\nNew Line 2a\nNew Line 2b\nNew Line 2c\nLine 3\nLine 4"
	assert.Equal(t, expectedContent, string(content))
}

func TestWriteFileTool_PartialWrite_FileNotExists(t *testing.T) {
	// Create temp directory
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "nonexistent.txt")

	tool := WriteFileTool{}
	input := `{"path": "` + strings.ReplaceAll(
		testFile,
		"\\",
		"\\\\",
	) + `", "content": "New content", "startLine": 1, "endLine": 1}`

	result, err := tool.Call(context.Background(), input)
	assert.NoError(t, err)

	// Should return error
	assert.Contains(t, result, "error")
	assert.Contains(t, result, "does not exist")
	assert.Contains(t, result, "Cannot perform partial write on file")
	assert.Contains(t, result, "For new files, omit startLine and endLine parameters")
}

func TestWriteFileTool_PartialWrite_InvalidLineNumbers(t *testing.T) {
	// Create temp directory and initial file
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.txt")

	err := os.WriteFile(testFile, []byte("Line 1\nLine 2"), 0600)
	require.NoError(t, err)

	tool := WriteFileTool{}

	// Test startLine provided but not endLine
	input := `{"path": "` + strings.ReplaceAll(testFile, "\\", "\\\\") + `", "content": "content", "startLine": 1}`
	result, err := tool.Call(context.Background(), input)
	assert.NoError(t, err)
	assert.Contains(t, result, "error")
	assert.Contains(t, result, "Both startLine and endLine must be provided")

	// Test endLine provided but not startLine
	input = `{"path": "` + strings.ReplaceAll(testFile, "\\", "\\\\") + `", "content": "content", "endLine": 1}`
	result, err = tool.Call(context.Background(), input)
	assert.NoError(t, err)
	assert.Contains(t, result, "error")
	assert.Contains(t, result, "Both startLine and endLine must be provided")

	// Test startLine < 1 (this will trigger the partial write validation)
	input = `{"path": "` + strings.ReplaceAll(
		testFile,
		"\\",
		"\\\\",
	) + `", "content": "content", "startLine": 0, "endLine": 1}`
	result, err = tool.Call(context.Background(), input)
	assert.NoError(t, err)
	assert.Contains(t, result, "error")
	assert.Contains(t, result, "Both startLine and endLine must be provided") // 0 is treated as "not provided"

	// Test valid line numbers but startLine > endLine
	input = `{"path": "` + strings.ReplaceAll(
		testFile,
		"\\",
		"\\\\",
	) + `", "content": "content", "startLine": 3, "endLine": 1}`
	result, err = tool.Call(context.Background(), input)
	assert.NoError(t, err)
	assert.Contains(t, result, "error")
	assert.Contains(t, result, "startLine cannot be greater than endLine")
}

func TestWriteFileTool_PartialWrite_BeyondFileLength(t *testing.T) {
	// Create temp directory and initial file
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.txt")

	// Create initial file with 3 lines
	initialContent := "Line 1\nLine 2\nLine 3"
	err := os.WriteFile(testFile, []byte(initialContent), 0600)
	require.NoError(t, err)

	tool := WriteFileTool{}
	// Try to replace lines 2-5 (beyond file length)
	input := `{"path": "` + strings.ReplaceAll(
		testFile,
		"\\",
		"\\\\",
	) + `", "content": "New content", "startLine": 2, "endLine": 5}`

	result, err := tool.Call(context.Background(), input)
	assert.NoError(t, err)

	// Verify response
	var response WriteFileResponse
	err = json.Unmarshal([]byte(result), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.True(t, response.IsPartial)

	// Verify file content - should append since endLine > file length
	content, err := os.ReadFile(testFile)
	assert.NoError(t, err)
	expectedContent := "Line 1\nNew content"
	assert.Equal(t, expectedContent, string(content))
}

func TestWriteFileTool_PartialWrite_PreserveLineEndings(t *testing.T) {
	// Create temp directory and initial file with Windows line endings
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.txt")

	// Create initial file with CRLF line endings
	initialContent := "Line 1\r\nLine 2\r\nLine 3\r\n"
	err := os.WriteFile(testFile, []byte(initialContent), 0600)
	require.NoError(t, err)

	tool := WriteFileTool{}
	input := `{"path": "` + strings.ReplaceAll(
		testFile,
		"\\",
		"\\\\",
	) + `", "content": "Modified Line 2", "startLine": 2, "endLine": 2}`

	result, err := tool.Call(context.Background(), input)
	assert.NoError(t, err)

	// Verify response
	var response WriteFileResponse
	err = json.Unmarshal([]byte(result), &response)
	require.NoError(t, err)
	assert.True(t, response.Success)

	// Verify file content preserves CRLF
	content, err := os.ReadFile(testFile)
	assert.NoError(t, err)
	expectedContent := "Line 1\r\nModified Line 2\r\nLine 3\r\n"
	assert.Equal(t, expectedContent, string(content))
	assert.Contains(t, string(content), "\r\n") // Verify CRLF preserved
}

func TestWriteFileTool_ProcessContent_EscapeSequences(t *testing.T) {
	tool := WriteFileTool{}

	// Test newline escape
	result := tool.processContent("Line 1\\nLine 2")
	assert.Equal(t, "Line 1\nLine 2", result)

	// Test tab escape
	result = tool.processContent("Column1\\tColumn2")
	assert.Equal(t, "Column1\tColumn2", result)

	// Test both
	result = tool.processContent("Line 1\\nColumn1\\tColumn2")
	assert.Equal(t, "Line 1\nColumn1\tColumn2", result)
}

func TestWriteFileTool_EnsureDirectory(t *testing.T) {
	tool := WriteFileTool{}
	tempDir := t.TempDir()

	// Test creating nested directory
	testFile := filepath.Join(tempDir, "subdir", "nested", "test.txt")
	err := tool.ensureDirectory(testFile)
	assert.NoError(t, err)

	// Verify directory exists
	dirPath := filepath.Dir(testFile)
	info, err := os.Stat(dirPath)
	assert.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestWriteFileTool_Integration_ComplexScenario(t *testing.T) {
	// Create temp directory
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "complex.txt")

	tool := WriteFileTool{}

	// Step 1: Create initial file
	input := `{"path": "` + strings.ReplaceAll(
		testFile,
		"\\",
		"\\\\",
	) + `", "content": "# Configuration File\nversion: 1.0\nname: test\nport: 8080\ndebug: false", "mode": "create"}`
	result, err := tool.Call(context.Background(), input)
	assert.NoError(t, err)
	assert.Contains(t, result, `"success": true`)

	// Step 2: Append new section
	input = `{"path": "` + strings.ReplaceAll(
		testFile,
		"\\",
		"\\\\",
	) + `", "content": "\n# Database Config\nhost: localhost\nport: 5432", "mode": "append"}`
	result, err = tool.Call(context.Background(), input)
	assert.NoError(t, err)
	assert.Contains(t, result, `"success": true`)

	// Step 3: Update specific lines (change port and debug)
	input = `{"path": "` + strings.ReplaceAll(
		testFile,
		"\\",
		"\\\\",
	) + `", "content": "port: 9090\ndebug: true", "startLine": 4, "endLine": 5}`
	result, err = tool.Call(context.Background(), input)
	assert.NoError(t, err)

	var response WriteFileResponse
	err = json.Unmarshal([]byte(result), &response)
	require.NoError(t, err)
	assert.True(t, response.Success)
	assert.True(t, response.IsPartial)

	// Verify final content
	content, err := os.ReadFile(testFile)
	assert.NoError(t, err)
	//nolint:lll
	expectedContent := "# Configuration File\nversion: 1.0\nname: test\nport: 9090\ndebug: true\n# Database Config\nhost: localhost\nport: 5432"
	assert.Equal(t, expectedContent, string(content))
}

func TestWriteFileTool_PartialWrite_InvalidLineRanges(t *testing.T) {
	// Create temp directory and initial file
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.txt")

	err := os.WriteFile(testFile, []byte("Line 1\nLine 2"), 0600)
	require.NoError(t, err)

	tool := WriteFileTool{}

	// Test negative startLine (will be handled by partial write validation)
	input := `{"path": "` + strings.ReplaceAll(
		testFile,
		"\\",
		"\\\\",
	) + `", "content": "content", "startLine": -1, "endLine": 1}`
	result, err := tool.Call(context.Background(), input)
	assert.NoError(t, err)
	assert.Contains(t, result, "error")
	assert.Contains(t, result, "startLine must be")

	// Test negative endLine
	input = `{"path": "` + strings.ReplaceAll(
		testFile,
		"\\",
		"\\\\",
	) + `", "content": "content", "startLine": 1, "endLine": -1}`
	result, err = tool.Call(context.Background(), input)
	assert.NoError(t, err)
	assert.Contains(t, result, "error")
	assert.Contains(t, result, "endLine must be")
}
