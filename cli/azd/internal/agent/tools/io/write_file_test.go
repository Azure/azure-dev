// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package io

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteFileTool_Name(t *testing.T) {
	tool, _ := createTestWriteTool(t)
	assert.Equal(t, "write_file", tool.Name())
}

func TestWriteFileTool_Description(t *testing.T) {
	tool, _ := createTestWriteTool(t)
	desc := tool.Description()
	assert.Contains(t, desc, "Comprehensive file writing tool")
	assert.Contains(t, desc, "partial")
	assert.Contains(t, desc, "startLine")
	assert.Contains(t, desc, "endLine")
}

func TestWriteFileTool_Call_EmptyInput(t *testing.T) {
	tool, _ := createTestWriteTool(t)
	result, err := tool.Call(context.Background(), "")

	assert.NoError(t, err)
	assert.Contains(t, result, "error")
	assert.Contains(t, result, "No input provided")
}

func TestWriteFileTool_Call_InvalidJSON(t *testing.T) {
	tool, _ := createTestWriteTool(t)
	result, err := tool.Call(context.Background(), "invalid json")

	assert.NoError(t, err)
	assert.Contains(t, result, "error")
	assert.Contains(t, result, "Invalid JSON input: Input does not appear to be valid JSON object")
}

func TestWriteFileTool_Call_MalformedJSON(t *testing.T) {
	tool, _ := createTestWriteTool(t)
	// Test with JSON that has parse errors
	result, err := tool.Call(context.Background(), `{"path": "test.txt", "content": "unclosed string}`)

	assert.NoError(t, err)
	assert.Contains(t, result, "error")
	assert.Contains(t, result, "Invalid JSON input. Error:")
	assert.Contains(t, result, "Input (first 200 chars):")
}

func TestWriteFileTool_Call_MissingFilename(t *testing.T) {
	tool, _ := createTestWriteTool(t)

	// Use struct with missing Path field
	request := WriteFileRequest{
		Content: "test content",
		// Path is intentionally missing
	}
	input := mustMarshalJSON(request)

	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)
	assert.Contains(t, result, "error")
	assert.Contains(t, result, "filename cannot be empty")
}

func TestWriteFileTool_FullFileWrite(t *testing.T) {
	// Create temp directory
	tool, tempDir := createTestWriteTool(t)
	testFile := filepath.Join(tempDir, "test.txt")

	request := WriteFileRequest{
		Path:    testFile,
		Content: "Hello, World!",
	}
	input := mustMarshalJSON(request)

	result, err := tool.Call(context.Background(), input)
	assert.NoError(t, err)

	// Verify response using struct
	var response WriteFileResponse
	err = json.Unmarshal([]byte(result), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.Equal(t, "Wrote", response.Operation)
	assert.True(t, strings.HasSuffix(response.Path, filepath.Base(testFile)))
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
	tool, tempDir := createTestWriteTool(t)
	testFile := filepath.Join(tempDir, "test.txt")

	// Create initial file
	err := os.WriteFile(testFile, []byte("Initial content"), 0600)
	require.NoError(t, err)

	request := WriteFileRequest{
		Path:    testFile,
		Content: "\nAppended content",
		Mode:    "append",
	}
	input := mustMarshalJSON(request)

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
	tool, tempDir := createTestWriteTool(t)
	testFile := filepath.Join(tempDir, "new-file.txt")

	request := WriteFileRequest{
		Path:    testFile,
		Content: "New file content",
		Mode:    "create",
	}
	input := mustMarshalJSON(request)

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
	tool, tempDir := createTestWriteTool(t)
	testFile := filepath.Join(tempDir, "existing.txt")

	err := os.WriteFile(testFile, []byte("Existing content"), 0600)
	require.NoError(t, err)

	request := WriteFileRequest{
		Path:    testFile,
		Content: "New content",
		Mode:    "create",
	}
	input := mustMarshalJSON(request)

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
	tool, tempDir := createTestWriteTool(t)
	testFile := filepath.Join(tempDir, "test.txt")

	// Create initial file with multiple lines
	initialContent := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"
	err := os.WriteFile(testFile, []byte(initialContent), 0600)
	require.NoError(t, err)

	request := WriteFileRequest{
		Path:      testFile,
		Content:   "Modified Line 2\nModified Line 3",
		StartLine: 2,
		EndLine:   3,
	}
	input := mustMarshalJSON(request)

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
	tool, tempDir := createTestWriteTool(t)
	testFile := filepath.Join(tempDir, "test.txt")

	// Create initial file
	initialContent := "Line 1\nLine 2\nLine 3"
	err := os.WriteFile(testFile, []byte(initialContent), 0600)
	require.NoError(t, err)

	request := WriteFileRequest{
		Path:      testFile,
		Content:   "Replaced Line 2",
		StartLine: 2,
		EndLine:   2,
	}
	input := mustMarshalJSON(request)

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
	tool, tempDir := createTestWriteTool(t)
	testFile := filepath.Join(tempDir, "test.txt")

	// Create initial file
	initialContent := "Line 1\nLine 2\nLine 3\nLine 4"
	err := os.WriteFile(testFile, []byte(initialContent), 0600)
	require.NoError(t, err)

	request := WriteFileRequest{
		Path:      testFile,
		Content:   "New Line 2a\nNew Line 2b\nNew Line 2c",
		StartLine: 2,
		EndLine:   2,
	}
	input := mustMarshalJSON(request)

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
	tool, tempDir := createTestWriteTool(t)
	testFile := filepath.Join(tempDir, "nonexistent.txt")

	request := WriteFileRequest{
		Path:      testFile,
		Content:   "New content",
		StartLine: 1,
		EndLine:   1,
	}
	input := mustMarshalJSON(request)

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
	tool, tempDir := createTestWriteTool(t)
	testFile := filepath.Join(tempDir, "test.txt")

	err := os.WriteFile(testFile, []byte("Line 1\nLine 2"), 0600)
	require.NoError(t, err)

	// Test startLine provided but not endLine
	request1 := WriteFileRequest{
		Path:      testFile,
		Content:   "content",
		StartLine: 1,
		// EndLine intentionally missing
	}
	input := mustMarshalJSON(request1)
	result, err := tool.Call(context.Background(), input)
	assert.NoError(t, err)
	assert.Contains(t, result, "error")
	assert.Contains(t, result, "Both startLine and endLine must be provided")

	// Test endLine provided but not startLine
	request2 := WriteFileRequest{
		Path:    testFile,
		Content: "content",
		EndLine: 1,
		// StartLine intentionally missing (will be 0)
	}
	input = mustMarshalJSON(request2)
	result, err = tool.Call(context.Background(), input)
	assert.NoError(t, err)
	assert.Contains(t, result, "error")
	assert.Contains(t, result, "Both startLine and endLine must be provided")

	// Test startLine < 1 (this will trigger the partial write validation)
	request3 := WriteFileRequest{
		Path:      testFile,
		Content:   "content",
		StartLine: 0,
		EndLine:   1,
	}
	input = mustMarshalJSON(request3)
	result, err = tool.Call(context.Background(), input)
	assert.NoError(t, err)
	assert.Contains(t, result, "error")
	assert.Contains(t, result, "Both startLine and endLine must be provided") // 0 is treated as "not provided"

	// Test valid line numbers but startLine > endLine
	request4 := WriteFileRequest{
		Path:      testFile,
		Content:   "content",
		StartLine: 3,
		EndLine:   1,
	}
	input = mustMarshalJSON(request4)
	result, err = tool.Call(context.Background(), input)
	assert.NoError(t, err)
	assert.Contains(t, result, "error")
	assert.Contains(t, result, "startLine cannot be greater than endLine")
}

func TestWriteFileTool_PartialWrite_BeyondFileLength(t *testing.T) {
	// Create temp directory and initial file
	tool, tempDir := createTestWriteTool(t)
	testFile := filepath.Join(tempDir, "test.txt")

	// Create initial file with 3 lines
	initialContent := "Line 1\nLine 2\nLine 3"
	err := os.WriteFile(testFile, []byte(initialContent), 0600)
	require.NoError(t, err)

	request := WriteFileRequest{
		Path:      testFile,
		Content:   "New content",
		StartLine: 2,
		EndLine:   5,
	}
	input := mustMarshalJSON(request)

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
	tool, tempDir := createTestWriteTool(t)
	testFile := filepath.Join(tempDir, "test.txt")

	// Create initial file with CRLF line endings
	initialContent := "Line 1\r\nLine 2\r\nLine 3\r\n"
	err := os.WriteFile(testFile, []byte(initialContent), 0600)
	require.NoError(t, err)

	request := WriteFileRequest{
		Path:      testFile,
		Content:   "Modified Line 2",
		StartLine: 2,
		EndLine:   2,
	}
	input := mustMarshalJSON(request)

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
	tool, _ := createTestWriteTool(t)

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
	tool, tempDir := createTestWriteTool(t)

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
	tool, tempDir := createTestWriteTool(t)
	testFile := filepath.Join(tempDir, "complex.txt")

	// Step 1: Create initial file
	request1 := WriteFileRequest{
		Path:    testFile,
		Content: "# Configuration File\nversion: 1.0\nname: test\nport: 8080\ndebug: false",
		Mode:    "create",
	}
	input := mustMarshalJSON(request1)
	result, err := tool.Call(context.Background(), input)
	assert.NoError(t, err)
	assert.Contains(t, result, `"success": true`)

	// Step 2: Append new section
	request2 := WriteFileRequest{
		Path:    testFile,
		Content: "\n# Database Config\nhost: localhost\nport: 5432",
		Mode:    "append",
	}
	input = mustMarshalJSON(request2)
	result, err = tool.Call(context.Background(), input)
	assert.NoError(t, err)
	assert.Contains(t, result, `"success": true`)

	// Step 3: Update specific lines (change port and debug)
	request3 := WriteFileRequest{
		Path:      testFile,
		Content:   "port: 9090\ndebug: true",
		StartLine: 4,
		EndLine:   5,
	}
	input = mustMarshalJSON(request3)
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
	tool, tempDir := createTestWriteTool(t)
	testFile := filepath.Join(tempDir, "test.txt")

	err := os.WriteFile(testFile, []byte("Line 1\nLine 2"), 0600)
	require.NoError(t, err)

	// Test negative startLine (will be handled by partial write validation)
	request1 := WriteFileRequest{
		Path:      testFile,
		Content:   "content",
		StartLine: -1,
		EndLine:   1,
	}
	input := mustMarshalJSON(request1)
	result, err := tool.Call(context.Background(), input)
	assert.NoError(t, err)
	assert.Contains(t, result, "error")
	assert.Contains(t, result, "startLine must be")

	// Test negative endLine
	request2 := WriteFileRequest{
		Path:      testFile,
		Content:   "content",
		StartLine: 1,
		EndLine:   -1,
	}
	input = mustMarshalJSON(request2)
	result, err = tool.Call(context.Background(), input)
	assert.NoError(t, err)
	assert.Contains(t, result, "error")
	assert.Contains(t, result, "endLine must be")
}

func TestWriteFileTool_SecurityBoundaryValidation(t *testing.T) {
	tool, tempDir := createTestWriteTool(t)

	tests := []struct {
		name          string
		path          string
		expectedError string
		shouldFail    bool
	}{
		{
			name:          "write file outside security root - absolute path",
			path:          absoluteOutsidePath("system"),
			expectedError: "Access denied: file write operation not permitted outside the allowed directory",
			shouldFail:    true,
		},
		{
			name:          "write file escaping with relative path",
			path:          relativeEscapePath("deep"),
			expectedError: "Access denied: file write operation not permitted outside the allowed directory",
			shouldFail:    true,
		},
		{
			name:          "write windows system file",
			path:          platformSpecificPath("hosts"),
			expectedError: "Access denied: file write operation not permitted outside the allowed directory",
			shouldFail:    true,
		},
		{
			name:          "write SSH private key",
			path:          platformSpecificPath("ssh_keys"),
			expectedError: "Access denied: file write operation not permitted outside the allowed directory",
			shouldFail:    true,
		},
		{
			name:          "write to startup folder",
			path:          platformSpecificPath("startup_folder"),
			expectedError: "Access denied: file write operation not permitted outside the allowed directory",
			shouldFail:    true,
		},
		{
			name:          "write shell configuration",
			path:          platformSpecificPath("shell_config"),
			expectedError: "Access denied: file write operation not permitted outside the allowed directory",
			shouldFail:    true,
		},
		{
			name:       "valid write within security root",
			path:       filepath.Join(tempDir, "test_file.txt"),
			shouldFail: false,
		},
		{
			name: "valid write subdirectory file within security root",
			path: filepath.Join(tempDir, "subdir", "test_file.txt"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Use proper JSON escaping for Windows paths
			input := fmt.Sprintf(`{"path": "%s", "content": "test content"}`, strings.ReplaceAll(tc.path, "\\", "\\\\"))
			result, err := tool.Call(context.Background(), input)

			// Tool calls should never return Go errors - they return JSON responses
			assert.NoError(t, err)
			assert.NotEmpty(t, result)

			if tc.shouldFail {
				// For security failures, expect JSON error response
				assert.Contains(t, result, `"error": true`)
				assert.Contains(t, result, tc.expectedError)
			} else {
				// For valid paths, expect either success or file/directory not found
				// but should NOT contain security error message
				expected := "Access denied: file write operation not permitted outside the allowed directory"
				assert.NotContains(t, result, expected)
			}
		})
	}
}
