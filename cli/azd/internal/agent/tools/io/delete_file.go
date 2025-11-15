// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package io

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/azure/azure-dev/internal/agent/security"
	"github.com/azure/azure-dev/internal/agent/tools/common"
	"github.com/mark3labs/mcp-go/mcp"
)

// DeleteFileTool implements the Tool interface for deleting files
type DeleteFileTool struct {
	common.BuiltInTool
	securityManager *security.Manager
}

func (t DeleteFileTool) Name() string {
	return "delete_file"
}

func (t DeleteFileTool) Annotations() mcp.ToolAnnotation {
	return mcp.ToolAnnotation{
		Title:           "Delete File",
		ReadOnlyHint:    common.ToPtr(false),
		DestructiveHint: common.ToPtr(true),
		IdempotentHint:  common.ToPtr(false),
		OpenWorldHint:   common.ToPtr(false),
	}
}

func (t DeleteFileTool) Description() string {
	return "Delete a file. Input: file path (e.g., 'temp.txt' or './docs/old-file.md')"
}

func (t DeleteFileTool) Call(ctx context.Context, input string) (string, error) {
	input = strings.TrimPrefix(input, `"`)
	input = strings.TrimSuffix(input, `"`)
	input = strings.TrimSpace(input)

	if input == "" {
		return common.CreateErrorResponse(fmt.Errorf("file path is required"), "File path is required")
	}

	// Security validation
	validatedPath, err := t.securityManager.ValidatePath(input)
	if err != nil {
		return common.CreateErrorResponse(
			err,
			"Access denied: file deletion operation not permitted outside the allowed directory",
		)
	}

	// Check if file exists and get info
	info, err := os.Stat(validatedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return common.CreateErrorResponse(err, fmt.Sprintf("File %s does not exist", validatedPath))
		}
		return common.CreateErrorResponse(err, fmt.Sprintf("Cannot access file %s: %s", validatedPath, err.Error()))
	}

	// Make sure it's a file, not a directory
	if info.IsDir() {
		return common.CreateErrorResponse(
			fmt.Errorf("%s is a directory, not a file", validatedPath),
			fmt.Sprintf("%s is a directory, not a file. Use delete_directory to remove directories", validatedPath),
		)
	}

	fileSize := info.Size()

	// Delete the file
	err = os.Remove(validatedPath)
	if err != nil {
		return common.CreateErrorResponse(err, fmt.Sprintf("Failed to delete file %s: %s", validatedPath, err.Error()))
	}

	// Create success response
	type DeleteFileResponse struct {
		Success     bool   `json:"success"`
		FilePath    string `json:"filePath"`
		SizeDeleted int64  `json:"sizeDeleted"`
		Message     string `json:"message"`
	}

	response := DeleteFileResponse{
		Success:     true,
		FilePath:    validatedPath,
		SizeDeleted: fileSize,
		Message:     fmt.Sprintf("Successfully deleted file %s (%d bytes)", validatedPath, fileSize),
	}

	// Convert to JSON
	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return common.CreateErrorResponse(err, fmt.Sprintf("Failed to marshal JSON response: %s", err.Error()))
	}

	return string(jsonData), nil
}
