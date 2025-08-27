// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package io

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
	"github.com/mark3labs/mcp-go/mcp"
)

// CopyFileRequest represents the JSON payload for file copy requests
type CopyFileRequest struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Overwrite   bool   `json:"overwrite,omitempty"` // Optional: allow overwriting existing files
}

// CopyFileResponse represents the JSON output for the copy_file tool
type CopyFileResponse struct {
	Success     bool   `json:"success"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	BytesCopied int64  `json:"bytesCopied"`
	Overwritten bool   `json:"overwritten"` // Indicates if an existing file was overwritten
	Message     string `json:"message"`
}

// CopyFileTool implements the Tool interface for copying files
type CopyFileTool struct {
	common.BuiltInTool
}

func (t CopyFileTool) Name() string {
	return "copy_file"
}

func (t CopyFileTool) Annotations() mcp.ToolAnnotation {
	return mcp.ToolAnnotation{
		Title:           "Copy File",
		ReadOnlyHint:    common.ToPtr(false),
		DestructiveHint: common.ToPtr(false),
		IdempotentHint:  common.ToPtr(true),
		OpenWorldHint:   common.ToPtr(false),
	}
}

func (t CopyFileTool) Description() string {
	return `Copy a file to a new location. By default, fails if destination already exists.
Input: JSON object with required 'source' and 'destination' fields, optional 'overwrite' field:
{"source": "file.txt", "destination": "backup.txt", "overwrite": false}

Fields:
- source: Path to the source file (required)
- destination: Path where the file should be copied (required)  
- overwrite: If true, allows overwriting existing destination file (optional, default: false)

Returns: JSON with copy operation details or error information.
The input must be formatted as a single line valid JSON string.

Examples:
- Safe copy: {"source": "data.txt", "destination": "backup.txt"}
- Copy with overwrite: {"source": "data.txt", "destination": "backup.txt", "overwrite": true}`
}

// createErrorResponse creates a JSON error response
func (t CopyFileTool) createErrorResponse(err error, message string) (string, error) {
	return common.CreateErrorResponse(err, message)
}

func (t CopyFileTool) Call(ctx context.Context, input string) (string, error) {
	var params CopyFileRequest

	// Clean the input first
	cleanInput := strings.TrimSpace(input)

	// Parse as JSON - this is now required
	if err := json.Unmarshal([]byte(cleanInput), &params); err != nil {
		return t.createErrorResponse(
			err,
			fmt.Sprintf(
				"Invalid JSON input: %s. Expected format: "+
					`{"source": "file.txt", "destination": "backup.txt", "overwrite": false}`,
				err.Error(),
			),
		)
	}

	source := strings.TrimSpace(params.Source)
	destination := strings.TrimSpace(params.Destination)

	if source == "" || destination == "" {
		return t.createErrorResponse(
			fmt.Errorf("both source and destination paths are required"),
			"Both source and destination paths are required",
		)
	}

	// Check if source file exists
	sourceInfo, err := os.Stat(source)
	if err != nil {
		return t.createErrorResponse(err, fmt.Sprintf("Source file %s does not exist: %s", source, err.Error()))
	}

	if sourceInfo.IsDir() {
		return t.createErrorResponse(
			fmt.Errorf("source %s is a directory", source),
			fmt.Sprintf("Source %s is a directory. Use copy_directory for directories", source),
		)
	}

	// Check if destination exists and handle overwrite logic
	destinationExisted := false
	if _, err := os.Stat(destination); err == nil {
		// Destination file exists
		destinationExisted = true
		if !params.Overwrite {
			return t.createErrorResponse(
				fmt.Errorf("destination file %s already exists", destination),
				fmt.Sprintf("Destination file %s already exists. Set \"overwrite\": true to allow overwriting", destination),
			)
		}
	}

	// Open source file
	sourceFile, err := os.Open(source)
	if err != nil {
		return t.createErrorResponse(err, fmt.Sprintf("Failed to open source file %s: %s", source, err.Error()))
	}
	defer sourceFile.Close()

	// Create destination file
	destFile, err := os.Create(destination)
	if err != nil {
		return t.createErrorResponse(err, fmt.Sprintf("Failed to create destination file %s: %s", destination, err.Error()))
	}
	defer destFile.Close()

	// Copy contents
	bytesWritten, err := io.Copy(destFile, sourceFile)
	if err != nil {
		return t.createErrorResponse(err, fmt.Sprintf("Failed to copy file: %s", err.Error()))
	}

	// Prepare JSON response structure
	// Determine if this was an overwrite operation
	wasOverwrite := destinationExisted && params.Overwrite

	var message string
	if wasOverwrite {
		message = fmt.Sprintf(
			"Successfully copied %s to %s (%d bytes) - overwrote existing file",
			source,
			destination,
			bytesWritten,
		)
	} else {
		message = fmt.Sprintf("Successfully copied %s to %s (%d bytes)", source, destination, bytesWritten)
	}

	response := CopyFileResponse{
		Success:     true,
		Source:      source,
		Destination: destination,
		BytesCopied: bytesWritten,
		Overwritten: wasOverwrite,
		Message:     message,
	}

	// Convert to JSON
	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return t.createErrorResponse(err, fmt.Sprintf("Failed to marshal JSON response: %s", err.Error()))
	}

	return string(jsonData), nil
}
