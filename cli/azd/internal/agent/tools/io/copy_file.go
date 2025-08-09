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
)

// CopyFileTool implements the Tool interface for copying files
type CopyFileTool struct {
	common.LocalTool
}

func (t CopyFileTool) Name() string {
	return "copy_file"
}

func (t CopyFileTool) Description() string {
	return `Copy a file to a new location. 
Input: JSON object with required 'source' and 'destination' fields: {"source": "file.txt", "destination": "backup.txt"}
Returns: JSON with copy operation details or error information.
The input must be formatted as a single line valid JSON string.`
}

// createErrorResponse creates a JSON error response
func (t CopyFileTool) createErrorResponse(err error, message string) (string, error) {
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

	return string(jsonData), nil
}

func (t CopyFileTool) Call(ctx context.Context, input string) (string, error) {
	// Parse JSON input
	type InputParams struct {
		Source      string `json:"source"`
		Destination string `json:"destination"`
	}

	var params InputParams

	// Clean the input first
	cleanInput := strings.TrimSpace(input)

	// Parse as JSON - this is now required
	if err := json.Unmarshal([]byte(cleanInput), &params); err != nil {
		return t.createErrorResponse(
			err,
			fmt.Sprintf(
				"Invalid JSON input: %s. Expected format: {\"source\": \"file.txt\", \"destination\": \"backup.txt\"}",
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
	type CopyResponse struct {
		Success     bool   `json:"success"`
		Source      string `json:"source"`
		Destination string `json:"destination"`
		BytesCopied int64  `json:"bytesCopied"`
		Message     string `json:"message"`
	}

	response := CopyResponse{
		Success:     true,
		Source:      source,
		Destination: destination,
		BytesCopied: bytesWritten,
		Message:     fmt.Sprintf("Successfully copied %s to %s (%d bytes)", source, destination, bytesWritten),
	}

	// Convert to JSON
	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return t.createErrorResponse(err, fmt.Sprintf("Failed to marshal JSON response: %s", err.Error()))
	}

	return string(jsonData), nil
}
