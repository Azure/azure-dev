// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package io

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/azure/azure-dev/internal/agent/security"
	"github.com/azure/azure-dev/internal/agent/tools/common"
	"github.com/mark3labs/mcp-go/mcp"
)

// ReadFileTool implements the Tool interface for reading file contents
type ReadFileTool struct {
	common.BuiltInTool
	securityManager *security.Manager
}

// ReadFileRequest represents the JSON payload for file read requests
type ReadFileRequest struct {
	Path      string `json:"path"`
	StartLine int    `json:"startLine,omitempty"` // Optional: 1-based line number to start reading from
	EndLine   int    `json:"endLine,omitempty"`   // Optional: 1-based line number to end reading at
}

// ReadFileResponse represents the JSON output for the read_file tool
type ReadFileResponse struct {
	Success     bool         `json:"success"`
	Path        string       `json:"path"`
	Content     string       `json:"content"`
	IsTruncated bool         `json:"isTruncated"`
	IsPartial   bool         `json:"isPartial"`
	LineRange   *LineRange   `json:"lineRange,omitempty"`
	FileInfo    ReadFileInfo `json:"fileInfo"`
	Message     string       `json:"message,omitempty"`
}

// LineRange represents the range of lines read
type LineRange struct {
	StartLine  int `json:"startLine"`
	EndLine    int `json:"endLine"`
	TotalLines int `json:"totalLines"`
	LinesRead  int `json:"linesRead"`
}

// ReadFileInfo represents file metadata for read operations
type ReadFileInfo struct {
	Size         int64     `json:"size"`
	ModifiedTime time.Time `json:"modifiedTime"`
	Permissions  string    `json:"permissions"`
}

func (t ReadFileTool) Name() string {
	return "read_file"
}

func (t ReadFileTool) Annotations() mcp.ToolAnnotation {
	return mcp.ToolAnnotation{
		Title:           "Read File Contents",
		ReadOnlyHint:    common.ToPtr(true),
		DestructiveHint: common.ToPtr(false),
		IdempotentHint:  common.ToPtr(true),
		OpenWorldHint:   common.ToPtr(false),
	}
}

func (t ReadFileTool) Description() string {
	return `Read file contents with intelligent handling for different file sizes and partial reads. 
Returns JSON response with file content and metadata.

Input: JSON payload with the following structure:
{
  "path": "path/to/file.txt",
  "startLine": 10,    // optional: 1-based line number to start reading from
  "endLine": 50       // optional: 1-based line number to end reading at
}

Examples:
1. Read entire file:
   {"path": "README.md"}

2. Read specific line range:
   {"path": "src/main.go", "startLine": 1, "endLine": 100}

3. Read from line to end:
   {"path": "config.go", "startLine": 25}

4. Read from start to line:
   {"path": "app.py", "endLine": 30}

5. Read single line:
   {"path": "package.json", "startLine": 42, "endLine": 42}

Files larger than 100KB are automatically truncated.
Files over 1MB show size info only unless specific line range is requested.
The input must be formatted as a single line valid JSON string.`
}

func (t ReadFileTool) Call(ctx context.Context, input string) (string, error) {
	if input == "" {
		return common.CreateErrorResponse(
			fmt.Errorf("empty input"),
			"No input provided. Expected JSON format: {\"filePath\": \"path/to/file.txt\"}",
		)
	}

	// Parse JSON input
	var req ReadFileRequest
	if err := json.Unmarshal([]byte(input), &req); err != nil {
		return common.CreateErrorResponse(
			err,
			fmt.Sprintf(
				"Invalid JSON input: %s. "+
					"Expected format: {\"filePath\": \"path/to/file.txt\", \"startLine\": 1, \"endLine\": 50}",
				err.Error(),
			),
		)
	}

	// Validate required fields
	if req.Path == "" {
		return common.CreateErrorResponse(fmt.Errorf("missing filePath"), "Missing required field: filePath cannot be empty")
	}

	// Security validation
	validatedPath, err := t.securityManager.ValidatePath(req.Path)
	if err != nil {
		return common.CreateErrorResponse(
			err,
			"Access denied: file read operation not permitted outside the allowed directory",
		)
	}

	// Get file info first to check size
	fileInfo, err := os.Stat(validatedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return common.CreateErrorResponse(
				err,
				fmt.Sprintf("File does not exist: %s. Please check file path spelling and location", validatedPath),
			)
		}
		return common.CreateErrorResponse(err, fmt.Sprintf("Cannot access file %s: %s", validatedPath, err.Error()))
	}

	if fileInfo.IsDir() {
		return common.CreateErrorResponse(
			fmt.Errorf("path is a directory"),
			fmt.Sprintf("%s is a directory, not a file. Use directory_list tool for directories", validatedPath),
		)
	}

	// Handle very large files (>1MB) - require line range
	const maxFileSize = 1024 * 1024 // 1MB
	if fileInfo.Size() > maxFileSize && req.StartLine == 0 && req.EndLine == 0 {
		return common.CreateErrorResponse(
			fmt.Errorf("file too large"),
			fmt.Sprintf(
				"File %s is too large (%d bytes). Please specify startLine and endLine to read specific sections",
				validatedPath,
				fileInfo.Size(),
			),
		)
	}

	// Read file content
	file, err := os.Open(validatedPath)
	if err != nil {
		return common.CreateErrorResponse(err, fmt.Sprintf("Failed to open file %s: %s", validatedPath, err.Error()))
	}
	defer file.Close()

	// Read lines
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return common.CreateErrorResponse(err, fmt.Sprintf("Error reading file %s: %s", validatedPath, err.Error()))
	}

	totalLines := len(lines)
	var content string
	var isPartial bool
	var isTruncated bool
	var lineRange *LineRange

	// Determine what to read
	if req.StartLine > 0 || req.EndLine > 0 {
		// Reading specific line range
		startLine := req.StartLine
		endLine := req.EndLine

		if startLine == 0 {
			startLine = 1
		}
		if endLine == 0 {
			endLine = totalLines
		}

		// Validate line range
		if startLine > totalLines {
			return common.CreateErrorResponse(
				fmt.Errorf("start line out of range"),
				fmt.Sprintf("Start line %d is greater than total lines %d in file", startLine, totalLines),
			)
		}
		if startLine > endLine {
			return common.CreateErrorResponse(
				fmt.Errorf("invalid line range"),
				fmt.Sprintf("Start line %d is greater than end line %d", startLine, endLine),
			)
		}

		// Adjust endLine if it exceeds total lines
		if endLine > totalLines {
			endLine = totalLines
		}

		// Convert to 0-based indexing and extract lines
		startIdx := startLine - 1
		endIdx := endLine
		selectedLines := lines[startIdx:endIdx]
		content = strings.Join(selectedLines, "\n")
		isPartial = true

		lineRange = &LineRange{
			StartLine:  startLine,
			EndLine:    endLine,
			TotalLines: totalLines,
			LinesRead:  endLine - startLine + 1,
		}
	} else {
		// Reading entire file
		content = strings.Join(lines, "\n")

		// Truncate if content is too large (>100KB)
		const maxContentSize = 100 * 1024 // 100KB
		if len(content) > maxContentSize {
			content = content[:maxContentSize] + "\n... [content truncated]"
			isTruncated = true
		}
	}

	// Create success response
	response := ReadFileResponse{
		Success:     true,
		Path:        validatedPath,
		Content:     content,
		IsTruncated: isTruncated,
		IsPartial:   isPartial,
		LineRange:   lineRange,
		FileInfo: ReadFileInfo{
			Size:         fileInfo.Size(),
			ModifiedTime: fileInfo.ModTime(),
			Permissions:  fileInfo.Mode().String(),
		},
	}

	// Set appropriate message
	if isPartial && lineRange != nil {
		response.Message = fmt.Sprintf(
			"Successfully read %d lines (%d-%d) from file",
			lineRange.LinesRead,
			lineRange.StartLine,
			lineRange.EndLine,
		)
	} else if isTruncated {
		response.Message = "Successfully read file (content truncated due to size)"
	} else {
		response.Message = fmt.Sprintf("Successfully read entire file (%d lines)", totalLines)
	}

	// Convert to JSON
	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return common.CreateErrorResponse(err, fmt.Sprintf("Failed to marshal JSON response: %s", err.Error()))
	}

	return string(jsonData), nil
}
