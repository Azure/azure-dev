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
	"time"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadFileTool_Name(t *testing.T) {
	tool, _ := createTestReadTool(t)
	assert.Equal(t, "read_file", tool.Name())
}

func TestReadFileTool_Description(t *testing.T) {
	tool, _ := createTestReadTool(t)
	desc := tool.Description()
	assert.Contains(t, desc, "Read file contents")
	assert.Contains(t, desc, "startLine")
	assert.Contains(t, desc, "endLine")
	assert.Contains(t, desc, "JSON")
}

func TestReadFileTool_Annotations(t *testing.T) {
	tool, _ := createTestReadTool(t)
	annotations := tool.Annotations()
	assert.Equal(t, "Read File Contents", annotations.Title)
	assert.NotNil(t, annotations.ReadOnlyHint)
	assert.True(t, *annotations.ReadOnlyHint)
	assert.NotNil(t, annotations.DestructiveHint)
	assert.False(t, *annotations.DestructiveHint)
	assert.NotNil(t, annotations.IdempotentHint)
	assert.True(t, *annotations.IdempotentHint)
	assert.NotNil(t, annotations.OpenWorldHint)
	assert.False(t, *annotations.OpenWorldHint)
}

func TestReadFileTool_Call_EmptyInput(t *testing.T) {
	tool, _ := createTestReadTool(t)
	result, err := tool.Call(context.Background(), "")

	assert.NoError(t, err)

	var errorResp common.ErrorResponse
	err = json.Unmarshal([]byte(result), &errorResp)
	require.NoError(t, err)

	assert.True(t, errorResp.Error)
	assert.Contains(t, errorResp.Message, "No input provided")
	assert.Contains(t, errorResp.Message, "JSON format")
}

func TestReadFileTool_Call_InvalidJSON(t *testing.T) {
	tool, _ := createTestReadTool(t)
	result, err := tool.Call(context.Background(), "invalid json")

	assert.NoError(t, err)

	var errorResp common.ErrorResponse
	err = json.Unmarshal([]byte(result), &errorResp)
	require.NoError(t, err)

	assert.True(t, errorResp.Error)
	assert.Contains(t, errorResp.Message, "Invalid JSON input")
}

func TestReadFileTool_Call_MalformedJSON(t *testing.T) {
	tool, _ := createTestReadTool(t)
	result, err := tool.Call(context.Background(), `{"path": "test.txt", "unclosed": "value}`)

	assert.NoError(t, err)

	var errorResp common.ErrorResponse
	err = json.Unmarshal([]byte(result), &errorResp)
	require.NoError(t, err)

	assert.True(t, errorResp.Error)
	assert.Contains(t, errorResp.Message, "Invalid JSON input")
}

func TestReadFileTool_Call_MissingFilePath(t *testing.T) {
	tool, _ := createTestReadTool(t)

	// Use struct with missing Path field
	request := ReadFileRequest{
		StartLine: 1,
		EndLine:   10,
		// Path is intentionally missing
	}
	input := mustMarshalJSON(request)

	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	var errorResp common.ErrorResponse
	err = json.Unmarshal([]byte(result), &errorResp)
	require.NoError(t, err)

	assert.True(t, errorResp.Error)
	assert.Contains(t, errorResp.Message, "filePath cannot be empty")
}

func TestReadFileTool_Call_EmptyFilePath(t *testing.T) {
	tool, _ := createTestReadTool(t)

	// Use struct with empty Path field
	request := ReadFileRequest{
		Path:      "",
		StartLine: 1,
	}
	input := mustMarshalJSON(request)

	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	var errorResp common.ErrorResponse
	err = json.Unmarshal([]byte(result), &errorResp)
	require.NoError(t, err)

	assert.True(t, errorResp.Error)
	assert.Contains(t, errorResp.Message, "filePath cannot be empty")
}

func TestReadFileTool_Call_FileNotFound(t *testing.T) {
	tool, _ := createTestReadTool(t)

	request := ReadFileRequest{
		Path: absoluteOutsidePath("system"),
	}
	input := mustMarshalJSON(request)

	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	var errorResp common.ErrorResponse
	err = json.Unmarshal([]byte(result), &errorResp)
	require.NoError(t, err)

	assert.True(t, errorResp.Error)
	assert.Contains(t, errorResp.Message, "Access denied")
	assert.Contains(t, errorResp.Message, "file read operation not permitted outside the allowed directory")
}

func TestReadFileTool_Call_DirectoryInsteadOfFile(t *testing.T) {
	tool, tempDir := createTestReadTool(t)

	input := fmt.Sprintf(`{"path": "%s"}`, strings.ReplaceAll(tempDir, "\\", "\\\\"))
	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	var errorResp common.ErrorResponse
	err = json.Unmarshal([]byte(result), &errorResp)
	require.NoError(t, err)

	assert.True(t, errorResp.Error)
	assert.Contains(t, errorResp.Message, "is a directory")
	assert.Contains(t, errorResp.Message, "directory_list tool")
}

func TestReadFileTool_ReadEntireSmallFile(t *testing.T) {
	tool, tempDir := createTestReadTool(t)
	testFile := filepath.Join(tempDir, "test.txt")
	testContent := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	request := ReadFileRequest{
		Path: testFile,
	}
	input := mustMarshalJSON(request)

	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	var response ReadFileResponse
	err = json.Unmarshal([]byte(result), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.True(t, strings.HasSuffix(response.Path, filepath.Base(testFile)))
	assert.Equal(t, testContent, response.Content)
	assert.False(t, response.IsTruncated)
	assert.False(t, response.IsPartial)
	assert.Nil(t, response.LineRange)
	assert.Contains(t, response.Message, "Successfully read entire file (5 lines)")
	assert.Greater(t, response.FileInfo.Size, int64(0))
	assert.False(t, response.FileInfo.ModifiedTime.IsZero())
	assert.NotEmpty(t, response.FileInfo.Permissions)
}

func TestReadFileTool_ReadSingleLine(t *testing.T) {
	tool, tempDir := createTestReadTool(t)
	testFile := filepath.Join(tempDir, "test.txt")
	testContent := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	request := ReadFileRequest{
		Path:      testFile,
		StartLine: 3,
		EndLine:   3,
	}
	input := mustMarshalJSON(request)

	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	var response ReadFileResponse
	err = json.Unmarshal([]byte(result), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.Equal(t, "Line 3", response.Content)
	assert.False(t, response.IsTruncated)
	assert.True(t, response.IsPartial)
	require.NotNil(t, response.LineRange)
	assert.Equal(t, 3, response.LineRange.StartLine)
	assert.Equal(t, 3, response.LineRange.EndLine)
	assert.Equal(t, 5, response.LineRange.TotalLines)
	assert.Equal(t, 1, response.LineRange.LinesRead)
	assert.Contains(t, response.Message, "Successfully read 1 lines (3-3)")
}

func TestReadFileTool_ReadMultipleLines(t *testing.T) {
	tool, tempDir := createTestReadTool(t)
	testFile := filepath.Join(tempDir, "test.txt")
	testContent := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	input := fmt.Sprintf(`{"path": "%s", "startLine": 2, "endLine": 4}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	var response ReadFileResponse
	err = json.Unmarshal([]byte(result), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.Equal(t, "Line 2\nLine 3\nLine 4", response.Content)
	assert.False(t, response.IsTruncated)
	assert.True(t, response.IsPartial)
	require.NotNil(t, response.LineRange)
	assert.Equal(t, 2, response.LineRange.StartLine)
	assert.Equal(t, 4, response.LineRange.EndLine)
	assert.Equal(t, 5, response.LineRange.TotalLines)
	assert.Equal(t, 3, response.LineRange.LinesRead)
	assert.Contains(t, response.Message, "Successfully read 3 lines (2-4)")
}

func TestReadFileTool_ReadFromStartToLine(t *testing.T) {
	tool, tempDir := createTestReadTool(t)
	testFile := filepath.Join(tempDir, "test.txt")
	testContent := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	input := fmt.Sprintf(`{"path": "%s", "endLine": 3}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	var response ReadFileResponse
	err = json.Unmarshal([]byte(result), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.Equal(t, "Line 1\nLine 2\nLine 3", response.Content)
	assert.True(t, response.IsPartial)
	require.NotNil(t, response.LineRange)
	assert.Equal(t, 1, response.LineRange.StartLine)
	assert.Equal(t, 3, response.LineRange.EndLine)
	assert.Equal(t, 3, response.LineRange.LinesRead)
}

func TestReadFileTool_ReadFromLineToEnd(t *testing.T) {
	tool, tempDir := createTestReadTool(t)
	testFile := filepath.Join(tempDir, "test.txt")
	testContent := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	input := fmt.Sprintf(`{"path": "%s", "startLine": 3}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	var response ReadFileResponse
	err = json.Unmarshal([]byte(result), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.Equal(t, "Line 3\nLine 4\nLine 5", response.Content)
	assert.True(t, response.IsPartial)
	require.NotNil(t, response.LineRange)
	assert.Equal(t, 3, response.LineRange.StartLine)
	assert.Equal(t, 5, response.LineRange.EndLine)
	assert.Equal(t, 3, response.LineRange.LinesRead)
}

func TestReadFileTool_StartLineOutOfRange(t *testing.T) {
	tool, tempDir := createTestReadTool(t)
	testFile := filepath.Join(tempDir, "test.txt")
	testContent := "Line 1\nLine 2\nLine 3"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	input := fmt.Sprintf(`{"path": "%s", "startLine": 10, "endLine": 15}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	var errorResp common.ErrorResponse
	err = json.Unmarshal([]byte(result), &errorResp)
	require.NoError(t, err)

	assert.True(t, errorResp.Error)
	assert.Contains(t, errorResp.Message, "Start line 10 is greater than total lines 3")
}

func TestReadFileTool_InvalidLineRange_StartGreaterThanEnd(t *testing.T) {
	tool, tempDir := createTestReadTool(t)
	testFile := filepath.Join(tempDir, "test.txt")
	testContent := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	input := fmt.Sprintf(`{"path": "%s", "startLine": 4, "endLine": 2}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	var errorResp common.ErrorResponse
	err = json.Unmarshal([]byte(result), &errorResp)
	require.NoError(t, err)

	assert.True(t, errorResp.Error)
	assert.Contains(t, errorResp.Message, "Start line 4 is greater than end line 2")
}

func TestReadFileTool_EndLineExceedsTotalLines(t *testing.T) {
	tool, tempDir := createTestReadTool(t)
	testFile := filepath.Join(tempDir, "test.txt")
	testContent := "Line 1\nLine 2\nLine 3"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	input := fmt.Sprintf(`{"path": "%s", "startLine": 2, "endLine": 10}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	var response ReadFileResponse
	err = json.Unmarshal([]byte(result), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.Equal(t, "Line 2\nLine 3", response.Content)
	assert.True(t, response.IsPartial)
	require.NotNil(t, response.LineRange)
	assert.Equal(t, 2, response.LineRange.StartLine)
	assert.Equal(t, 3, response.LineRange.EndLine) // Adjusted to total lines
	assert.Equal(t, 2, response.LineRange.LinesRead)
}

func TestReadFileTool_EmptyFile(t *testing.T) {
	tool, tempDir := createTestReadTool(t)
	testFile := filepath.Join(tempDir, "empty.txt")

	err := os.WriteFile(testFile, []byte(""), 0600)
	require.NoError(t, err)

	input := fmt.Sprintf(`{"path": "%s"}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	var response ReadFileResponse
	err = json.Unmarshal([]byte(result), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.Equal(t, "", response.Content)
	assert.False(t, response.IsTruncated)
	assert.False(t, response.IsPartial)
	assert.Contains(t, response.Message, "Successfully read entire file (0 lines)")
}

func TestReadFileTool_SingleLineFile(t *testing.T) {
	tool, tempDir := createTestReadTool(t)
	testFile := filepath.Join(tempDir, "single.txt")
	testContent := "Only one line"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	input := fmt.Sprintf(`{"path": "%s"}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	var response ReadFileResponse
	err = json.Unmarshal([]byte(result), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.Equal(t, testContent, response.Content)
	assert.False(t, response.IsTruncated)
	assert.False(t, response.IsPartial)
	assert.Contains(t, response.Message, "Successfully read entire file (1 lines)")
}

func TestReadFileTool_FileWithOnlyNewlines(t *testing.T) {
	tool, tempDir := createTestReadTool(t)
	testFile := filepath.Join(tempDir, "newlines.txt")
	testContent := "\n\n\n"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	input := fmt.Sprintf(`{"path": "%s"}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	var response ReadFileResponse
	err = json.Unmarshal([]byte(result), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.Equal(t, "\n\n", response.Content) // 3 empty lines joined with newlines = 2 newlines
	assert.False(t, response.IsTruncated)
	assert.False(t, response.IsPartial)
	assert.Contains(t, response.Message, "Successfully read entire file (3 lines)")
}

func TestReadFileTool_LargeFileWithoutLineRange(t *testing.T) {
	tool, tempDir := createTestReadTool(t)
	testFile := filepath.Join(tempDir, "large.txt")

	// Create a file larger than 1MB
	largeContent := strings.Repeat("This is a line that will be repeated many times to create a large file.\n", 20000)
	err := os.WriteFile(testFile, []byte(largeContent), 0600)
	require.NoError(t, err)

	// Verify file is actually large
	fileInfo, err := os.Stat(testFile)
	require.NoError(t, err)
	require.Greater(t, fileInfo.Size(), int64(1024*1024)) // Greater than 1MB

	input := fmt.Sprintf(`{"path": "%s"}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	var errorResp common.ErrorResponse
	err = json.Unmarshal([]byte(result), &errorResp)
	require.NoError(t, err)

	assert.True(t, errorResp.Error)
	assert.Contains(t, errorResp.Message, "File")
	assert.Contains(t, errorResp.Message, "is too large")
	assert.Contains(t, errorResp.Message, "specify startLine and endLine")
}

func TestReadFileTool_LargeFileWithLineRange(t *testing.T) {
	tool, tempDir := createTestReadTool(t)
	testFile := filepath.Join(tempDir, "large.txt")

	// Create a file larger than 1MB
	largeContent := strings.Repeat("This is line content that will be repeated many times.\n", 20000)
	err := os.WriteFile(testFile, []byte(largeContent), 0600)
	require.NoError(t, err)

	// Verify file is actually large
	fileInfo, err := os.Stat(testFile)
	require.NoError(t, err)
	require.Greater(t, fileInfo.Size(), int64(1024*1024)) // Greater than 1MB

	input := fmt.Sprintf(`{"path": "%s", "startLine": 100, "endLine": 102}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	var response ReadFileResponse
	err = json.Unmarshal([]byte(result), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.True(t, response.IsPartial)
	require.NotNil(t, response.LineRange)
	assert.Equal(t, 100, response.LineRange.StartLine)
	assert.Equal(t, 102, response.LineRange.EndLine)
	assert.Equal(t, 3, response.LineRange.LinesRead)
	assert.Contains(t, response.Content, "This is line content")
}

func TestReadFileTool_ContentTruncation(t *testing.T) {
	tool, tempDir := createTestReadTool(t)
	testFile := filepath.Join(tempDir, "medium.txt")

	// Create content that exceeds 100KB (truncation threshold)
	lineContent := strings.Repeat("A", 1000) // 1KB per line
	lines := make([]string, 150)             // 150KB total
	for i := range lines {
		lines[i] = fmt.Sprintf("Line %d: %s", i+1, lineContent)
	}
	content := strings.Join(lines, "\n")

	err := os.WriteFile(testFile, []byte(content), 0600)
	require.NoError(t, err)

	input := fmt.Sprintf(`{"path": "%s"}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	var response ReadFileResponse
	err = json.Unmarshal([]byte(result), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.True(t, response.IsTruncated)
	assert.False(t, response.IsPartial)
	assert.Contains(t, response.Content, "[content truncated]")
	assert.Contains(t, response.Message, "content truncated due to size")
	assert.Less(t, len(response.Content), len(content)) // Should be shorter than original
}

func TestReadFileTool_SpecialCharacters(t *testing.T) {
	tool, tempDir := createTestReadTool(t)
	testFile := filepath.Join(tempDir, "special.txt")
	testContent := "Line with émojis 😀🎉\nLine with unicode: ñáéíóú\n" +
		"Line with symbols: @#$%^&*()\nLine with tabs:\t\tand\tspaces"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	input := fmt.Sprintf(`{"path": "%s"}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	var response ReadFileResponse
	err = json.Unmarshal([]byte(result), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.Equal(t, testContent, response.Content)
	assert.Contains(t, response.Content, "😀🎉")
	assert.Contains(t, response.Content, "ñáéíóú")
	assert.Contains(t, response.Content, "\t")
}

func TestReadFileTool_WindowsLineEndings(t *testing.T) {
	tool, tempDir := createTestReadTool(t)
	testFile := filepath.Join(tempDir, "windows.txt")
	// Use Windows line endings (CRLF)
	testContent := "Line 1\r\nLine 2\r\nLine 3\r\n"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	input := fmt.Sprintf(`{"path": "%s", "startLine": 2, "endLine": 2}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	var response ReadFileResponse
	err = json.Unmarshal([]byte(result), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	// The scanner should handle CRLF properly and return just "Line 2"
	assert.Equal(t, "Line 2", response.Content)
	assert.Equal(t, 3, response.LineRange.TotalLines)
}

func TestReadFileTool_FileInfoMetadata(t *testing.T) {
	tool, tempDir := createTestReadTool(t)
	testFile := filepath.Join(tempDir, "metadata.txt")
	testContent := "Test content for metadata"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	// Get file info for comparison
	expectedInfo, err := os.Stat(testFile)
	require.NoError(t, err)

	input := fmt.Sprintf(`{"path": "%s"}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	var response ReadFileResponse
	err = json.Unmarshal([]byte(result), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.Equal(t, expectedInfo.Size(), response.FileInfo.Size)
	assert.Equal(t, expectedInfo.Mode().String(), response.FileInfo.Permissions)

	// Check that modification time is within a reasonable range (within 1 minute)
	timeDiff := response.FileInfo.ModifiedTime.Sub(expectedInfo.ModTime())
	assert.Less(t, timeDiff, time.Minute)
	assert.Greater(t, timeDiff, -time.Minute)
}

func TestReadFileTool_JSONResponseStructure(t *testing.T) {
	tool, tempDir := createTestReadTool(t)
	testFile := filepath.Join(tempDir, "json_test.txt")
	testContent := "Line 1\nLine 2"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	input := fmt.Sprintf(`{"path": "%s", "startLine": 1, "endLine": 1}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	// Test that result is valid JSON
	var jsonResult map[string]interface{}
	err = json.Unmarshal([]byte(result), &jsonResult)
	require.NoError(t, err)

	// Check required fields exist
	assert.Contains(t, jsonResult, "success")
	assert.Contains(t, jsonResult, "path")
	assert.Contains(t, jsonResult, "content")
	assert.Contains(t, jsonResult, "isTruncated")
	assert.Contains(t, jsonResult, "isPartial")
	assert.Contains(t, jsonResult, "lineRange")
	assert.Contains(t, jsonResult, "fileInfo")
	assert.Contains(t, jsonResult, "message")

	// Verify JSON formatting (should be indented)
	assert.Contains(t, result, "\n") // Should have newlines for formatting
	assert.Contains(t, result, "  ") // Should have indentation
}

func TestReadFileTool_ZeroBasedToOneBasedConversion(t *testing.T) {
	tool, tempDir := createTestReadTool(t)
	testFile := filepath.Join(tempDir, "indexing.txt")
	testContent := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	// Test reading line 1 (should be "Line 1", not "Line 2")
	input := fmt.Sprintf(`{"path": "%s", "startLine": 1, "endLine": 1}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	var response ReadFileResponse
	err = json.Unmarshal([]byte(result), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.Equal(t, "Line 1", response.Content)
	assert.Equal(t, 1, response.LineRange.StartLine)
	assert.Equal(t, 1, response.LineRange.EndLine)
}

func TestReadFileTool_SecurityBoundaryValidation(t *testing.T) {
	tests := []struct {
		name          string
		setupPath     string
		requestPath   string
		expectError   bool
		errorContains string
	}{
		{
			name:        "absolute path outside security root",
			requestPath: absoluteOutsidePath("system"),
			expectError: true,
		},
		{
			name:        "parent directory attempt with invalid file",
			requestPath: relativeEscapePath("with_file"),
			expectError: true,
		},
		{
			name:        "windows absolute path outside security root",
			requestPath: platformSpecificPath("hosts"),
			expectError: true,
		},
		{
			name:        "mixed separators escaping",
			requestPath: relativeEscapePath("mixed"),
			expectError: true,
		},
		{
			name:        "valid file within security root",
			setupPath:   "test.txt",
			requestPath: "test.txt",
			expectError: false,
		},
		{
			name:        "valid file in subdirectory within security root",
			setupPath:   "subdir/test.txt",
			requestPath: "subdir/test.txt",
			expectError: false,
		},
		{
			name:        "current directory reference within security root",
			setupPath:   "test.txt",
			requestPath: "./test.txt",
			expectError: false,
		},
		{
			name:        "parent directory attempt with valid file",
			setupPath:   "test.txt",
			requestPath: "subdir/../test.txt",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test environment
			tool, tempDir := createTestReadTool(t)

			// Setup test file if needed
			if tt.setupPath != "" {
				testFilePath := filepath.Join(tempDir, tt.setupPath)

				// Create subdirectory if needed
				dir := filepath.Dir(testFilePath)
				if dir != tempDir {
					err := os.MkdirAll(dir, 0755)
					require.NoError(t, err)
				}

				err := os.WriteFile(testFilePath, []byte("test content"), 0600)
				require.NoError(t, err)
			}

			// Create request
			request := ReadFileRequest{
				Path: tt.requestPath,
			}
			input := mustMarshalJSON(request)

			// Execute
			result, err := tool.Call(context.Background(), input)
			assert.NoError(t, err) // Call itself should not error

			if tt.expectError {
				// Should return error response
				var errorResp common.ErrorResponse
				err = json.Unmarshal([]byte(result), &errorResp)
				require.NoError(t, err)
				assert.True(t, errorResp.Error)
				assert.Contains(t, errorResp.Message, tt.errorContains)
			} else {
				// Should return success response
				var response ReadFileResponse
				err = json.Unmarshal([]byte(result), &response)
				require.NoError(t, err)
				assert.True(t, response.Success)
				assert.Equal(t, "test content", response.Content)
			}
		})
	}
}

func TestReadFileTool_SymlinkResolution(t *testing.T) {
	// Skip symlink tests on Windows as they require admin privileges
	if strings.Contains(strings.ToLower(os.Getenv("OS")), "windows") {
		t.Skip("Skipping symlink tests on Windows")
	}

	t.Run("symlink to existing file within security root", func(t *testing.T) {
		// Create test environment
		sm, tempDir := createTestSecurityManager(t)
		tool := ReadFileTool{securityManager: sm}

		// Create a real file
		realFile := filepath.Join(tempDir, "real_file.txt")
		err := os.WriteFile(realFile, []byte("symlink content"), 0600)
		require.NoError(t, err)

		// Create a symlink to the real file
		symlinkFile := filepath.Join(tempDir, "symlink_file.txt")
		err = os.Symlink(realFile, symlinkFile)
		require.NoError(t, err)

		// Test reading through the symlink
		request := ReadFileRequest{
			Path: symlinkFile,
		}
		input := mustMarshalJSON(request)

		result, err := tool.Call(context.Background(), input)
		assert.NoError(t, err)

		var response ReadFileResponse
		err = json.Unmarshal([]byte(result), &response)
		require.NoError(t, err)
		assert.True(t, response.Success)
		assert.Equal(t, "symlink content", response.Content)
	})

	t.Run("symlink to nonexistent file within security root", func(t *testing.T) {
		// Create test environment
		sm, tempDir := createTestSecurityManager(t)
		tool := ReadFileTool{securityManager: sm}

		// Create a symlink to a nonexistent file
		symlinkFile := filepath.Join(tempDir, "broken_symlink.txt")
		nonexistentFile := filepath.Join(tempDir, "nonexistent.txt")
		err := os.Symlink(nonexistentFile, symlinkFile)
		require.NoError(t, err)

		// Test reading through the broken symlink
		request := ReadFileRequest{
			Path: symlinkFile,
		}
		input := mustMarshalJSON(request)

		result, err := tool.Call(context.Background(), input)
		assert.NoError(t, err)

		var errorResp common.ErrorResponse
		err = json.Unmarshal([]byte(result), &errorResp)
		require.NoError(t, err)
		assert.True(t, errorResp.Error)
		assert.Contains(t, errorResp.Message, "File does not exist")
	})

	t.Run("symlink to file outside security root", func(t *testing.T) {
		// Create test environment
		sm, tempDir := createTestSecurityManager(t)
		tool := ReadFileTool{securityManager: sm}

		// Create a file outside the security root
		outsideDir := t.TempDir()
		outsideFile := filepath.Join(outsideDir, "outside.txt")
		err := os.WriteFile(outsideFile, []byte("outside content"), 0600)
		require.NoError(t, err)

		// Create a symlink inside the security root pointing to the outside file
		symlinkFile := filepath.Join(tempDir, "malicious_symlink.txt")
		err = os.Symlink(outsideFile, symlinkFile)
		require.NoError(t, err)

		// Test reading through the malicious symlink should fail
		request := ReadFileRequest{
			Path: symlinkFile,
		}
		input := mustMarshalJSON(request)

		result, err := tool.Call(context.Background(), input)
		assert.NoError(t, err)

		var errorResp common.ErrorResponse
		err = json.Unmarshal([]byte(result), &errorResp)
		require.NoError(t, err)
		assert.True(t, errorResp.Error)
		assert.Contains(t, errorResp.Message, "Access denied")
	})

	t.Run("symlinked security root directory", func(t *testing.T) {
		// Create security manager with the symlinked directory
		sm, realTempDir := createTestSecurityManager(t)

		// Create a symlink to the real temp directory
		symlinkTempDir := filepath.Join(t.TempDir(), "symlinked_root")
		err := os.Symlink(realTempDir, symlinkTempDir)
		require.NoError(t, err)

		tool := ReadFileTool{securityManager: sm}

		// Create a file in the real directory
		testFile := filepath.Join(realTempDir, "test.txt")
		err = os.WriteFile(testFile, []byte("test content"), 0600)
		require.NoError(t, err)

		// Test reading through the symlinked security root
		request := ReadFileRequest{
			Path: filepath.Join(symlinkTempDir, "test.txt"),
		}
		input := mustMarshalJSON(request)

		result, err := tool.Call(context.Background(), input)
		assert.NoError(t, err)

		var response ReadFileResponse
		err = json.Unmarshal([]byte(result), &response)
		require.NoError(t, err)
		assert.True(t, response.Success)
		assert.Equal(t, "test content", response.Content)
	})

	t.Run("complex symlink chain within security root", func(t *testing.T) {
		// Create test environment
		sm, tempDir := createTestSecurityManager(t)
		tool := ReadFileTool{securityManager: sm}

		// Create a real file
		realFile := filepath.Join(tempDir, "real.txt")
		err := os.WriteFile(realFile, []byte("chain content"), 0600)
		require.NoError(t, err)

		// Create a chain of symlinks
		link1 := filepath.Join(tempDir, "link1.txt")
		err = os.Symlink(realFile, link1)
		require.NoError(t, err)

		link2 := filepath.Join(tempDir, "link2.txt")
		err = os.Symlink(link1, link2)
		require.NoError(t, err)

		// Test reading through the symlink chain
		request := ReadFileRequest{
			Path: link2,
		}
		input := mustMarshalJSON(request)

		result, err := tool.Call(context.Background(), input)
		assert.NoError(t, err)

		var response ReadFileResponse
		err = json.Unmarshal([]byte(result), &response)
		require.NoError(t, err)
		assert.True(t, response.Success)
		assert.Equal(t, "chain content", response.Content)
	})
}

func TestReadFileTool_SecurityBoundaryEdgeCases(t *testing.T) {
	t.Run("empty path", func(t *testing.T) {
		tool, _ := createTestReadTool(t)

		request := ReadFileRequest{
			Path: "",
		}
		input := mustMarshalJSON(request)

		result, err := tool.Call(context.Background(), input)
		assert.NoError(t, err)

		var errorResp common.ErrorResponse
		err = json.Unmarshal([]byte(result), &errorResp)
		require.NoError(t, err)
		assert.True(t, errorResp.Error)
		assert.Contains(t, errorResp.Message, "filePath cannot be empty")
	})

	t.Run("null bytes in path", func(t *testing.T) {
		tool, _ := createTestReadTool(t)

		request := ReadFileRequest{
			Path: "test\x00file.txt",
		}
		input := mustMarshalJSON(request)

		result, err := tool.Call(context.Background(), input)
		assert.NoError(t, err)

		var errorResp common.ErrorResponse
		err = json.Unmarshal([]byte(result), &errorResp)
		require.NoError(t, err)
		assert.True(t, errorResp.Error)
	})
}

func TestReadFileTool_SecurityBoundaryWithDirectSecurityManager(t *testing.T) {
	// Test direct security manager interaction
	sm, _ := createTestSecurityManager(t)
	tool := ReadFileTool{securityManager: sm}

	// Test various malicious paths using cross-platform helper
	maliciousPaths := []string{
		relativeEscapePath("deep"),        // Relative path escape attempt
		"test.txt\x00../../../etc/passwd", // Null byte injection attack
		platformSpecificPath("users_dir"), // SSH keys or sensitive files
		absoluteOutsidePath("system"),     // System files (SAM/passwd)
		relativeEscapePath("mixed"),       // Home directory escape
		platformSpecificPath("hosts"),     // Absolute path outside security root
	}

	for _, maliciousPath := range maliciousPaths {
		t.Run("malicious_path_"+maliciousPath, func(t *testing.T) {
			request := ReadFileRequest{
				Path: maliciousPath,
			}
			input := mustMarshalJSON(request)

			result, err := tool.Call(context.Background(), input)
			assert.NoError(t, err)

			var errorResp common.ErrorResponse
			err = json.Unmarshal([]byte(result), &errorResp)
			require.NoError(t, err)
			assert.True(t, errorResp.Error)
			assert.Contains(t, errorResp.Message, "Access denied")
		})
	}
}
