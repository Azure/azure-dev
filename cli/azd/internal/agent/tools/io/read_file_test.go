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

// Helper function to marshal request structs to JSON strings
func mustMarshalJSON(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("Failed to marshal JSON: %v", err))
	}
	return string(data)
}

func TestReadFileTool_Name(t *testing.T) {
	tool := ReadFileTool{}
	assert.Equal(t, "read_file", tool.Name())
}

func TestReadFileTool_Description(t *testing.T) {
	tool := ReadFileTool{}
	desc := tool.Description()
	assert.Contains(t, desc, "Read file contents")
	assert.Contains(t, desc, "startLine")
	assert.Contains(t, desc, "endLine")
	assert.Contains(t, desc, "JSON")
}

func TestReadFileTool_Annotations(t *testing.T) {
	tool := ReadFileTool{}
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
	tool := ReadFileTool{}
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
	tool := ReadFileTool{}
	result, err := tool.Call(context.Background(), "invalid json")

	assert.NoError(t, err)

	var errorResp common.ErrorResponse
	err = json.Unmarshal([]byte(result), &errorResp)
	require.NoError(t, err)

	assert.True(t, errorResp.Error)
	assert.Contains(t, errorResp.Message, "Invalid JSON input")
}

func TestReadFileTool_Call_MalformedJSON(t *testing.T) {
	tool := ReadFileTool{}
	result, err := tool.Call(context.Background(), `{"filePath": "test.txt", "unclosed": "value}`)

	assert.NoError(t, err)

	var errorResp common.ErrorResponse
	err = json.Unmarshal([]byte(result), &errorResp)
	require.NoError(t, err)

	assert.True(t, errorResp.Error)
	assert.Contains(t, errorResp.Message, "Invalid JSON input")
}

func TestReadFileTool_Call_MissingFilePath(t *testing.T) {
	tool := ReadFileTool{}
	input := `{"startLine": 1, "endLine": 10}`
	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	var errorResp common.ErrorResponse
	err = json.Unmarshal([]byte(result), &errorResp)
	require.NoError(t, err)

	assert.True(t, errorResp.Error)
	assert.Contains(t, errorResp.Message, "filePath cannot be empty")
}

func TestReadFileTool_Call_EmptyFilePath(t *testing.T) {
	tool := ReadFileTool{}
	input := `{"filePath": "", "startLine": 1}`
	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	var errorResp common.ErrorResponse
	err = json.Unmarshal([]byte(result), &errorResp)
	require.NoError(t, err)

	assert.True(t, errorResp.Error)
	assert.Contains(t, errorResp.Message, "filePath cannot be empty")
}

func TestReadFileTool_Call_FileNotFound(t *testing.T) {
	tool := ReadFileTool{}
	input := `{"filePath": "/nonexistent/file.txt"}`
	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	var errorResp common.ErrorResponse
	err = json.Unmarshal([]byte(result), &errorResp)
	require.NoError(t, err)

	assert.True(t, errorResp.Error)
	assert.Contains(t, errorResp.Message, "File does not exist")
	assert.Contains(t, errorResp.Message, "check file path spelling")
}

func TestReadFileTool_Call_DirectoryInsteadOfFile(t *testing.T) {
	tempDir := t.TempDir()

	tool := ReadFileTool{}
	input := fmt.Sprintf(`{"filePath": "%s"}`, strings.ReplaceAll(tempDir, "\\", "\\\\"))
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
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.txt")
	testContent := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	tool := ReadFileTool{}
	input := fmt.Sprintf(`{"filePath": "%s"}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	var response ReadFileResponse
	err = json.Unmarshal([]byte(result), &response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.Equal(t, testFile, response.FilePath)
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
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.txt")
	testContent := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	tool := ReadFileTool{}
	input := fmt.Sprintf(`{"filePath": "%s", "startLine": 3, "endLine": 3}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
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
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.txt")
	testContent := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	tool := ReadFileTool{}
	input := fmt.Sprintf(`{"filePath": "%s", "startLine": 2, "endLine": 4}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
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
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.txt")
	testContent := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	tool := ReadFileTool{}
	input := fmt.Sprintf(`{"filePath": "%s", "endLine": 3}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
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
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.txt")
	testContent := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	tool := ReadFileTool{}
	input := fmt.Sprintf(`{"filePath": "%s", "startLine": 3}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
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
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.txt")
	testContent := "Line 1\nLine 2\nLine 3"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	tool := ReadFileTool{}
	input := fmt.Sprintf(`{"filePath": "%s", "startLine": 10, "endLine": 15}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	var errorResp common.ErrorResponse
	err = json.Unmarshal([]byte(result), &errorResp)
	require.NoError(t, err)

	assert.True(t, errorResp.Error)
	assert.Contains(t, errorResp.Message, "Start line 10 is greater than total lines 3")
}

func TestReadFileTool_InvalidLineRange_StartGreaterThanEnd(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.txt")
	testContent := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	tool := ReadFileTool{}
	input := fmt.Sprintf(`{"filePath": "%s", "startLine": 4, "endLine": 2}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	var errorResp common.ErrorResponse
	err = json.Unmarshal([]byte(result), &errorResp)
	require.NoError(t, err)

	assert.True(t, errorResp.Error)
	assert.Contains(t, errorResp.Message, "Start line 4 is greater than end line 2")
}

func TestReadFileTool_EndLineExceedsTotalLines(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.txt")
	testContent := "Line 1\nLine 2\nLine 3"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	tool := ReadFileTool{}
	input := fmt.Sprintf(`{"filePath": "%s", "startLine": 2, "endLine": 10}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
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
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "empty.txt")

	err := os.WriteFile(testFile, []byte(""), 0600)
	require.NoError(t, err)

	tool := ReadFileTool{}
	input := fmt.Sprintf(`{"filePath": "%s"}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
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
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "single.txt")
	testContent := "Only one line"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	tool := ReadFileTool{}
	input := fmt.Sprintf(`{"filePath": "%s"}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
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
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "newlines.txt")
	testContent := "\n\n\n"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	tool := ReadFileTool{}
	input := fmt.Sprintf(`{"filePath": "%s"}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
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
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "large.txt")

	// Create a file larger than 1MB
	largeContent := strings.Repeat("This is a line that will be repeated many times to create a large file.\n", 20000)
	err := os.WriteFile(testFile, []byte(largeContent), 0600)
	require.NoError(t, err)

	// Verify file is actually large
	fileInfo, err := os.Stat(testFile)
	require.NoError(t, err)
	require.Greater(t, fileInfo.Size(), int64(1024*1024)) // Greater than 1MB

	tool := ReadFileTool{}
	input := fmt.Sprintf(`{"filePath": "%s"}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
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
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "large.txt")

	// Create a file larger than 1MB
	largeContent := strings.Repeat("This is line content that will be repeated many times.\n", 20000)
	err := os.WriteFile(testFile, []byte(largeContent), 0600)
	require.NoError(t, err)

	// Verify file is actually large
	fileInfo, err := os.Stat(testFile)
	require.NoError(t, err)
	require.Greater(t, fileInfo.Size(), int64(1024*1024)) // Greater than 1MB

	tool := ReadFileTool{}
	input := fmt.Sprintf(`{"filePath": "%s", "startLine": 100, "endLine": 102}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
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
	tempDir := t.TempDir()
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

	tool := ReadFileTool{}
	input := fmt.Sprintf(`{"filePath": "%s"}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
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
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "special.txt")
	testContent := "Line with émojis 😀🎉\nLine with unicode: ñáéíóú\n" +
		"Line with symbols: @#$%^&*()\nLine with tabs:\t\tand\tspaces"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	tool := ReadFileTool{}
	input := fmt.Sprintf(`{"filePath": "%s"}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
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
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "windows.txt")
	// Use Windows line endings (CRLF)
	testContent := "Line 1\r\nLine 2\r\nLine 3\r\n"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	tool := ReadFileTool{}
	input := fmt.Sprintf(`{"filePath": "%s", "startLine": 2, "endLine": 2}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
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
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "metadata.txt")
	testContent := "Test content for metadata"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	// Get file info for comparison
	expectedInfo, err := os.Stat(testFile)
	require.NoError(t, err)

	tool := ReadFileTool{}
	input := fmt.Sprintf(`{"filePath": "%s"}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
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
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "json_test.txt")
	testContent := "Line 1\nLine 2"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	tool := ReadFileTool{}
	input := fmt.Sprintf(`{"filePath": "%s", "startLine": 1, "endLine": 1}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
	result, err := tool.Call(context.Background(), input)

	assert.NoError(t, err)

	// Test that result is valid JSON
	var jsonResult map[string]interface{}
	err = json.Unmarshal([]byte(result), &jsonResult)
	require.NoError(t, err)

	// Check required fields exist
	assert.Contains(t, jsonResult, "success")
	assert.Contains(t, jsonResult, "filePath")
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
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "indexing.txt")
	testContent := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"

	err := os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	tool := ReadFileTool{}

	// Test reading line 1 (should be "Line 1", not "Line 2")
	input := fmt.Sprintf(`{"filePath": "%s", "startLine": 1, "endLine": 1}`, strings.ReplaceAll(testFile, "\\", "\\\\"))
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
