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
	"time"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
)

// WriteFileTool implements a comprehensive file writing tool that handles all scenarios
type WriteFileTool struct {
	common.LocalTool
}

// WriteFileRequest represents the JSON input for the write_file tool
type WriteFileRequest struct {
	Filename  string `json:"filename"`
	Content   string `json:"content"`
	Mode      string `json:"mode,omitempty"`      // "write" (default), "append", "create"
	StartLine int    `json:"startLine,omitempty"` // For partial write: 1-based line number (inclusive)
	EndLine   int    `json:"endLine,omitempty"`   // For partial write: 1-based line number (inclusive)
}

// WriteFileResponse represents the JSON output for the write_file tool
type WriteFileResponse struct {
	Success      bool            `json:"success"`
	Operation    string          `json:"operation"`
	FilePath     string          `json:"filePath"`
	BytesWritten int             `json:"bytesWritten"`
	IsPartial    bool            `json:"isPartial"`          // True for partial write
	LineInfo     *LineInfo       `json:"lineInfo,omitempty"` // For partial write
	FileInfo     FileInfoDetails `json:"fileInfo"`
	Message      string          `json:"message,omitempty"`
}

// LineInfo represents line-based partial write details
type LineInfo struct {
	StartLine    int `json:"startLine"`
	EndLine      int `json:"endLine"`
	LinesChanged int `json:"linesChanged"`
}

// FileInfoDetails represents file metadata
type FileInfoDetails struct {
	Size         int64     `json:"size"`
	ModifiedTime time.Time `json:"modifiedTime"`
	Permissions  string    `json:"permissions"`
}

func (t WriteFileTool) Name() string {
	return "write_file"
}

func (t WriteFileTool) Description() string {
	return `Comprehensive file writing tool that handles full file writes, appends, and line-based partial updates.
Returns JSON response with operation details.

Input: JSON payload with the following structure:
{
  "filename": "path/to/file.txt",
  "content": "file content here",
  "mode": "write",
  "startLine": 5,
  "endLine": 8
}

Field descriptions:
- mode: "write" (default), "append", or "create"
- startLine: for partial write - 1-based line number (inclusive) - REQUIRES EXISTING FILE
- endLine: for partial write - 1-based line number (inclusive) - REQUIRES EXISTING FILE

MODES:
- "write" (default): Full file overwrite/create, OR partial line replacement when startLine/endLine provided
- "append": Add content to end of existing file
- "create": Create file only if it doesn't exist

PARTIAL WRITES (line-based editing):
⚠️  IMPORTANT: Partial writes REQUIRE an existing file. Cannot create new files with line positioning.
Add startLine and endLine to any "write" operation to replace specific lines in EXISTING files:
- Both are 1-based and inclusive
- startLine=5, endLine=8 replaces lines 5, 6, 7, and 8
- If endLine > file length, content is appended
- File MUST exist for partial writes - use regular write mode for new files

EXAMPLES:

Full file write (new or existing file):
{"filename": "./main.bicep", "content": "param location string = 'eastus'"}

Append to file:
{"filename": "./log.txt", "content": "\nNew log entry", "mode": "append"}

Partial write (replace specific lines in EXISTING file):
{"filename": "./config.json", "content": "  \"newSetting\": true,\n  \"version\": \"2.0\"", "startLine": 3, "endLine": 4}

Create only if doesn't exist:
{"filename": "./new-file.txt", "content": "Initial content", "mode": "create"}

The input must be formatted as a single line valid JSON string.`
}

// createErrorResponse creates a JSON error response
func (t WriteFileTool) createErrorResponse(err error, message string) (string, error) {
	if message == "" {
		message = err.Error()
	}

	errorResp := common.ErrorResponse{
		Error:   true,
		Message: message,
	}

	jsonData, jsonErr := json.MarshalIndent(errorResp, "", "  ")
	if jsonErr != nil {
		// Fallback to simple error message if JSON marshalling fails
		fallbackMsg := fmt.Sprintf(`{"error": true, "message": "JSON marshalling failed: %s"}`, jsonErr.Error())

		return fallbackMsg, nil
	}

	output := string(jsonData)

	return output, nil
}

func (t WriteFileTool) Call(ctx context.Context, input string) (string, error) {
	if input == "" {
		return t.createErrorResponse(fmt.Errorf("empty input"), "No input provided.")
	}

	// Debug: Check for common JSON issues
	input = strings.TrimSpace(input)
	if !strings.HasPrefix(input, "{") || !strings.HasSuffix(input, "}") {
		return t.createErrorResponse(
			fmt.Errorf("malformed JSON structure"),
			fmt.Sprintf(
				"Invalid JSON input: Input does not appear to be valid JSON object. Starts with: %q, Ends with: %q",
				input[:min(10, len(input))],
				input[max(0, len(input)-10):],
			),
		)
	}

	// Parse JSON input
	var req WriteFileRequest
	if err := json.Unmarshal([]byte(input), &req); err != nil {
		// Enhanced error reporting for debugging
		truncatedInput := input
		if len(input) > 200 {
			truncatedInput = input[:200] + "...[truncated]"
		}
		return t.createErrorResponse(
			err,
			fmt.Sprintf("Invalid JSON input. Error: %s. Input (first 200 chars): %s", err.Error(), truncatedInput),
		)
	}

	// Validate required fields
	if req.Filename == "" {
		return t.createErrorResponse(fmt.Errorf("missing filename"), "Missing required field: filename cannot be empty.")
	}

	// Determine mode and operation
	mode := req.Mode
	if mode == "" {
		mode = "write"
	}

	// Check if line numbers are provided for partial write
	hasStartLine := req.StartLine != 0
	hasEndLine := req.EndLine != 0

	// If any line number is provided, both must be provided and valid
	if hasStartLine || hasEndLine {
		if !hasStartLine || !hasEndLine {
			return t.createErrorResponse(
				fmt.Errorf("both startLine and endLine must be provided for partial write"),
				"Both startLine and endLine must be provided for partial write",
			)
		}

		// Validate that file exists for partial write BEFORE attempting
		filePath := strings.TrimSpace(req.Filename)
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			return t.createErrorResponse(
				err,
				fmt.Sprintf(
					"Cannot perform partial write on file '%s' because it does not exist. "+
						"For new files, omit startLine and endLine parameters to create the entire file",
					filePath,
				),
			)
		}

		// Smart write mode: this should be a partial write
		if mode == "write" {
			return t.handlePartialWrite(ctx, req)
		} else {
			return t.createErrorResponse(
				fmt.Errorf("startLine and endLine can only be used with write mode"),
				"startLine and endLine can only be used with write mode",
			)
		}
	}

	// Handle regular writing
	return t.handleRegularWrite(ctx, req, mode)
}

// handlePartialWrite handles line-based partial file editing
func (t WriteFileTool) handlePartialWrite(ctx context.Context, req WriteFileRequest) (string, error) {
	// Validate line numbers
	if req.StartLine < 1 {
		return t.createErrorResponse(fmt.Errorf("invalid startLine: %d", req.StartLine), "startLine must be >= 1")
	}
	if req.EndLine < 1 {
		return t.createErrorResponse(fmt.Errorf("invalid endLine: %d", req.EndLine), "endLine must be >= 1")
	}
	if req.StartLine > req.EndLine {
		return t.createErrorResponse(
			fmt.Errorf("invalid line range: startLine=%d > endLine=%d", req.StartLine, req.EndLine),
			"startLine cannot be greater than endLine",
		)
	}

	filePath := strings.TrimSpace(req.Filename)

	// Read existing file
	fileBytes, err := os.ReadFile(filePath)
	if err != nil {
		return t.createErrorResponse(err, fmt.Sprintf("Failed to read existing file %s: %s", filePath, err.Error()))
	}

	// Detect line ending style from existing content
	content := string(fileBytes)
	lineEnding := "\n"
	if strings.Contains(content, "\r\n") {
		lineEnding = "\r\n"
	} else if strings.Contains(content, "\r") {
		lineEnding = "\r"
	}

	// Split into lines (preserve line endings)
	lines := strings.Split(content, lineEnding)
	originalLineCount := len(lines)

	// Handle the case where file ends with line ending (empty last element)
	if originalLineCount > 0 && lines[originalLineCount-1] == "" {
		lines = lines[:originalLineCount-1]
		originalLineCount--
	}

	// Process new content
	newContent := t.processContent(req.Content)
	newLines := strings.Split(newContent, "\n")

	// If endLine is beyond file length, we'll append
	actualEndLine := req.EndLine
	if req.EndLine > originalLineCount {
		actualEndLine = originalLineCount
	}

	// Build new file content
	var result []string

	// Lines before the replacement
	if req.StartLine > 1 {
		result = append(result, lines[:req.StartLine-1]...)
	}

	// New lines
	result = append(result, newLines...)

	// Lines after the replacement (if any)
	if actualEndLine < originalLineCount {
		result = append(result, lines[actualEndLine:]...)
	}

	// Join with original line ending style
	finalContent := strings.Join(result, lineEnding)

	// If original file had trailing newline, preserve it
	if len(fileBytes) > 0 &&
		(string(fileBytes[len(fileBytes)-1:]) == "\n" || strings.HasSuffix(string(fileBytes), lineEnding)) {
		finalContent += lineEnding
	}

	// Write the updated content
	err = os.WriteFile(filePath, []byte(finalContent), 0600)
	if err != nil {
		return t.createErrorResponse(
			err,
			fmt.Sprintf("Failed to write updated content to file %s: %s", filePath, err.Error()),
		)
	}

	// Get file info
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return t.createErrorResponse(err, fmt.Sprintf("Failed to verify file %s: %s", filePath, err.Error()))
	}

	// Calculate lines changed
	linesChanged := len(newLines)

	// Create JSON response
	response := WriteFileResponse{
		Success:      true,
		Operation:    "Wrote (partial)",
		FilePath:     filePath,
		BytesWritten: len(newContent),
		IsPartial:    true,
		LineInfo: &LineInfo{
			StartLine:    req.StartLine,
			EndLine:      req.EndLine,
			LinesChanged: linesChanged,
		},
		FileInfo: FileInfoDetails{
			Size:         fileInfo.Size(),
			ModifiedTime: fileInfo.ModTime(),
			Permissions:  fileInfo.Mode().String(),
		},
		Message: fmt.Sprintf("Partial write completed: lines %d-%d replaced successfully", req.StartLine, req.EndLine),
	}

	// Convert to JSON
	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return t.createErrorResponse(err, fmt.Sprintf("Failed to marshal JSON response: %s", err.Error()))
	}

	return string(jsonData), nil
}

// handleRegularWrite handles normal file writing
func (t WriteFileTool) handleRegularWrite(ctx context.Context, req WriteFileRequest, mode string) (string, error) {
	filePath := strings.TrimSpace(req.Filename)
	content := t.processContent(req.Content)

	// Provide feedback for large content
	if len(content) > 10000 {
		fmt.Printf(
			"📝 Large content detected (%d chars). Consider breaking into smaller edits for better reliability.\n",
			len(content),
		)
	}

	// Ensure directory exists
	if err := t.ensureDirectory(filePath); err != nil {
		return t.createErrorResponse(err, fmt.Sprintf("Failed to create directory for file %s: %s", filePath, err.Error()))
	}

	var err error
	var operation string

	switch mode {
	case "create":
		if _, err := os.Stat(filePath); err == nil {
			return t.createErrorResponse(
				fmt.Errorf("file %s already exists (create mode)", filePath),
				fmt.Sprintf(
					"File %s already exists. Cannot create file in 'create' mode when file already exists",
					filePath,
				),
			)
		}
		err = os.WriteFile(filePath, []byte(content), 0600)
		operation = "Created"

	case "append":
		file, openErr := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if openErr != nil {
			return t.createErrorResponse(
				openErr,
				fmt.Sprintf("Failed to open file for append %s: %s", filePath, openErr.Error()),
			)
		}
		defer file.Close()
		_, err = file.WriteString(content)
		operation = "Appended to"

	default: // "write"
		err = os.WriteFile(filePath, []byte(content), 0600)
		operation = "Wrote"
	}

	if err != nil {
		return t.createErrorResponse(
			err,
			fmt.Sprintf("Failed to %s file %s: %s", strings.ToLower(operation), filePath, err.Error()),
		)
	}

	// Get file size for verification
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return t.createErrorResponse(err, fmt.Sprintf("Failed to verify file %s: %s", filePath, err.Error()))
	}

	// Create JSON response
	response := WriteFileResponse{
		Success:      true,
		Operation:    operation,
		FilePath:     filePath,
		BytesWritten: len(content),
		IsPartial:    false,
		FileInfo: FileInfoDetails{
			Size:         fileInfo.Size(),
			ModifiedTime: fileInfo.ModTime(),
			Permissions:  fileInfo.Mode().String(),
		},
		Message: fmt.Sprintf("File %s successfully", strings.ToLower(operation)),
	}

	// Convert to JSON
	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return t.createErrorResponse(err, fmt.Sprintf("Failed to marshal JSON response: %s", err.Error()))
	}

	output := string(jsonData)

	return output, nil
}

// processContent handles escape sequences
func (t WriteFileTool) processContent(content string) string {
	content = strings.ReplaceAll(content, "\\n", "\n")
	content = strings.ReplaceAll(content, "\\t", "\t")
	return content
}

// ensureDirectory creates the directory if it doesn't exist
func (t WriteFileTool) ensureDirectory(filePath string) error {
	dir := filepath.Dir(filePath)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}
	return nil
}

// Helper functions
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
