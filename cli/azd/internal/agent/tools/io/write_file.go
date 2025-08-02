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
type WriteFileTool struct{}

// WriteFileRequest represents the JSON input for the write_file tool
type WriteFileRequest struct {
	Filename    string `json:"filename"`
	Content     string `json:"content"`
	Mode        string `json:"mode,omitempty"`        // "write" (default), "append", "create"
	ChunkNum    int    `json:"chunkNum,omitempty"`    // For chunked writing: 1-based chunk number
	TotalChunks int    `json:"totalChunks,omitempty"` // For chunked writing: total expected chunks
}

// WriteFileResponse represents the JSON output for the write_file tool
type WriteFileResponse struct {
	Success      bool            `json:"success"`
	Operation    string          `json:"operation"`
	FilePath     string          `json:"filePath"`
	BytesWritten int             `json:"bytesWritten"`
	IsChunked    bool            `json:"isChunked"`
	ChunkInfo    *ChunkInfo      `json:"chunkInfo,omitempty"`
	FileInfo     FileInfoDetails `json:"fileInfo"`
	Message      string          `json:"message,omitempty"`
}

// ChunkInfo represents chunked writing details
type ChunkInfo struct {
	ChunkNumber int  `json:"chunkNumber"`
	TotalChunks int  `json:"totalChunks"`
	IsComplete  bool `json:"isComplete"`
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
	return `Comprehensive file writing tool that handles small and large files intelligently. Returns JSON response with operation details.

Input: JSON payload with the following structure:
{
  "filename": "path/to/file.txt",
  "content": "file content here",
  "mode": "write",
  "chunkNum": 1,
  "totalChunks": 3
}

Field descriptions:
- mode: "write" (default), "append", or "create"  
- chunkNum: for chunked writing (1-based)
- totalChunks: total number of chunks

MODES:
- "write" (default): Overwrite/create file
- "append": Add content to end of existing file
- "create": Create file only if it doesn't exist

CHUNKED WRITING (for large files):
Use chunkNum and totalChunks for files that might be too large:
- chunkNum: 1-based chunk number (1, 2, 3...)
- totalChunks: Total number of chunks you'll send

EXAMPLES:

Simple write:
{"filename": "./main.bicep", "content": "param location string = 'eastus'"}

Append to file:
{"filename": "./log.txt", "content": "\nNew log entry", "mode": "append"}

Large file (chunked):
{"filename": "./large.bicep", "content": "first part...", "chunkNum": 1, "totalChunks": 3}
{"filename": "./large.bicep", "content": "middle part...", "chunkNum": 2, "totalChunks": 3}
{"filename": "./large.bicep", "content": "final part...", "chunkNum": 3, "totalChunks": 3}

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

	// Parse JSON input
	var req WriteFileRequest
	if err := json.Unmarshal([]byte(input), &req); err != nil {
		return t.createErrorResponse(err, "Invalid JSON input")
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

	// Handle chunked writing
	isChunked := req.ChunkNum > 0 && req.TotalChunks > 0
	if isChunked {
		return t.handleChunkedWrite(ctx, req)
	}

	// Handle regular writing
	return t.handleRegularWrite(ctx, req, mode)
}

// handleChunkedWrite handles writing files in chunks
func (t WriteFileTool) handleChunkedWrite(ctx context.Context, req WriteFileRequest) (string, error) {
	if req.ChunkNum < 1 || req.TotalChunks < 1 || req.ChunkNum > req.TotalChunks {
		return t.createErrorResponse(fmt.Errorf("invalid chunk numbers: chunkNum=%d, totalChunks=%d", req.ChunkNum, req.TotalChunks), fmt.Sprintf("Invalid chunk numbers: chunkNum=%d, totalChunks=%d. ChunkNum must be between 1 and totalChunks", req.ChunkNum, req.TotalChunks))
	}

	filePath := strings.TrimSpace(req.Filename)
	content := t.processContent(req.Content)

	// Ensure directory exists
	if err := t.ensureDirectory(filePath); err != nil {
		return t.createErrorResponse(err, fmt.Sprintf("Failed to create directory for file %s: %s", filePath, err.Error()))
	}

	var err error
	var operation string

	if req.ChunkNum == 1 {
		// First chunk - create/overwrite file
		err = os.WriteFile(filePath, []byte(content), 0644)
		operation = "write"
	} else {
		// Subsequent chunks - append
		file, openErr := os.OpenFile(filePath, os.O_APPEND|os.O_WRONLY, 0644)
		if openErr != nil {
			return t.createErrorResponse(openErr, fmt.Sprintf("Failed to open file for append %s: %s", filePath, openErr.Error()))
		}
		defer file.Close()

		_, err = file.WriteString(content)
		operation = "append"
	}

	if err != nil {
		return t.createErrorResponse(err, fmt.Sprintf("Failed to write chunk to file %s: %s", filePath, err.Error()))
	}

	// Get file info
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
		IsChunked:    true,
		ChunkInfo: &ChunkInfo{
			ChunkNumber: req.ChunkNum,
			TotalChunks: req.TotalChunks,
			IsComplete:  req.ChunkNum == req.TotalChunks,
		},
		FileInfo: FileInfoDetails{
			Size:         fileInfo.Size(),
			ModifiedTime: fileInfo.ModTime(),
			Permissions:  fileInfo.Mode().String(),
		},
	}

	if req.ChunkNum == req.TotalChunks {
		response.Message = "File writing completed successfully"
	} else {
		response.Message = fmt.Sprintf("Chunk %d/%d written successfully", req.ChunkNum, req.TotalChunks)
	}

	// Convert to JSON
	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return t.createErrorResponse(err, fmt.Sprintf("Failed to marshal JSON response: %s", err.Error()))
	}

	output := string(jsonData)

	return output, nil
}

// handleRegularWrite handles normal file writing
func (t WriteFileTool) handleRegularWrite(ctx context.Context, req WriteFileRequest, mode string) (string, error) {
	filePath := strings.TrimSpace(req.Filename)
	content := t.processContent(req.Content)

	// Provide feedback for large content
	if len(content) > 10000 {
		fmt.Printf("📝 Large content detected (%d chars). Consider using chunked writing for better reliability.\n", len(content))
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
			return t.createErrorResponse(fmt.Errorf("file %s already exists (create mode)", filePath), fmt.Sprintf("File %s already exists. Cannot create file in 'create' mode when file already exists", filePath))
		}
		err = os.WriteFile(filePath, []byte(content), 0644)
		operation = "Created"

	case "append":
		file, openErr := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if openErr != nil {
			return t.createErrorResponse(openErr, fmt.Sprintf("Failed to open file for append %s: %s", filePath, openErr.Error()))
		}
		defer file.Close()
		_, err = file.WriteString(content)
		operation = "Appended to"

	default: // "write"
		err = os.WriteFile(filePath, []byte(content), 0644)
		operation = "Wrote"
	}

	if err != nil {
		return t.createErrorResponse(err, fmt.Sprintf("Failed to %s file %s: %s", strings.ToLower(operation), filePath, err.Error()))
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
		IsChunked:    false,
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
