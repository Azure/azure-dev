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

	"github.com/azure/azure-dev/cli/azd/internal/agent/security"
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
	securityManager *security.Manager
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

func (t CopyFileTool) Call(ctx context.Context, input string) (string, error) {
	var params CopyFileRequest

	// Clean the input first
	cleanInput := strings.TrimSpace(input)

	// Parse as JSON - this is now required
	if err := json.Unmarshal([]byte(cleanInput), &params); err != nil {
		return common.CreateErrorResponse(
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
		return common.CreateErrorResponse(
			fmt.Errorf("both source and destination paths are required"),
			"Both source and destination paths are required",
		)
	}

	// Security validation for both paths
	validatedSource, err := t.securityManager.ValidatePath(source)
	if err != nil {
		return common.CreateErrorResponse(
			err,
			"Access denied: source path operation not permitted outside the allowed directory",
		)
	}

	validatedDest, err := t.securityManager.ValidatePath(destination)
	if err != nil {
		return common.CreateErrorResponse(
			err,
			"Access denied: destination path operation not permitted outside the allowed directory",
		)
	}

	// Check if source file exists
	sourceInfo, err := os.Stat(validatedSource)
	if err != nil {
		return common.CreateErrorResponse(
			err,
			fmt.Sprintf("Source file %s does not exist: %s", validatedSource, err.Error()),
		)
	}

	if sourceInfo.IsDir() {
		return common.CreateErrorResponse(
			fmt.Errorf("source %s is a directory", validatedSource),
			fmt.Sprintf("Source %s is a directory. Use copy_directory for directories", validatedSource),
		)
	}

	// Check if destination exists and handle overwrite logic
	destinationExisted := false
	if _, err := os.Stat(validatedDest); err == nil {
		// Destination file exists
		destinationExisted = true
		if !params.Overwrite {
			return common.CreateErrorResponse(
				fmt.Errorf("destination file %s already exists", validatedDest),
				fmt.Sprintf(
					"Destination file %s already exists. Set \"overwrite\": true to allow overwriting",
					validatedDest,
				),
			)
		}
	}

	// Open source file
	sourceFile, err := os.Open(validatedSource)
	if err != nil {
		return common.CreateErrorResponse(
			err,
			fmt.Sprintf("Failed to open source file %s: %s", validatedSource, err.Error()),
		)
	}
	defer sourceFile.Close()

	// Create destination file
	destFile, err := os.Create(validatedDest)
	if err != nil {
		return common.CreateErrorResponse(
			err,
			fmt.Sprintf("Failed to create destination file %s: %s", validatedDest, err.Error()),
		)
	}
	defer destFile.Close()

	// Copy contents
	bytesWritten, err := io.Copy(destFile, sourceFile)
	if err != nil {
		return common.CreateErrorResponse(err, fmt.Sprintf("Failed to copy file: %s", err.Error()))
	}

	// Prepare JSON response structure
	// Determine if this was an overwrite operation
	wasOverwrite := destinationExisted && params.Overwrite

	var message string
	if wasOverwrite {
		message = fmt.Sprintf(
			"Successfully copied %s to %s (%d bytes) - overwrote existing file",
			validatedSource,
			validatedDest,
			bytesWritten,
		)
	} else {
		message = fmt.Sprintf("Successfully copied %s to %s (%d bytes)", validatedSource, validatedDest, bytesWritten)
	}

	response := CopyFileResponse{
		Success:     true,
		Source:      validatedSource,
		Destination: validatedDest,
		BytesCopied: bytesWritten,
		Overwritten: wasOverwrite,
		Message:     message,
	}

	// Convert to JSON
	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return common.CreateErrorResponse(err, fmt.Sprintf("Failed to marshal JSON response: %s", err.Error()))
	}

	return string(jsonData), nil
}
