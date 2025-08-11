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

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
	"github.com/mark3labs/mcp-go/mcp"
)

// ChangeDirectoryTool implements the Tool interface for changing the current working directory
type ChangeDirectoryTool struct {
	common.BuiltInTool
}

func (t ChangeDirectoryTool) Name() string {
	return "change_directory"
}

func (t ChangeDirectoryTool) Annotations() mcp.ToolAnnotation {
	return mcp.ToolAnnotation{
		Title:           "Change Directory",
		ReadOnlyHint:    common.ToPtr(false),
		DestructiveHint: common.ToPtr(false),
		IdempotentHint:  common.ToPtr(true),
		OpenWorldHint:   common.ToPtr(false),
	}
}

func (t ChangeDirectoryTool) Description() string {
	return "Change the current working directory. " +
		"Input: directory path (e.g., '../parent' or './subfolder' or absolute path)"
}

// createErrorResponse creates a JSON error response
func (t ChangeDirectoryTool) createErrorResponse(err error, message string) (string, error) {
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

func (t ChangeDirectoryTool) Call(ctx context.Context, input string) (string, error) {
	input = strings.TrimSpace(input)
	input = strings.Trim(input, `"`)

	if input == "" {
		return t.createErrorResponse(fmt.Errorf("directory path is required"), "Directory path is required")
	}

	// Convert to absolute path
	absPath, err := filepath.Abs(input)
	if err != nil {
		return t.createErrorResponse(err, fmt.Sprintf("Failed to resolve path %s: %s", input, err.Error()))
	}

	// Check if directory exists
	info, err := os.Stat(absPath)
	if err != nil {
		return t.createErrorResponse(err, fmt.Sprintf("Directory %s does not exist: %s", absPath, err.Error()))
	}
	if !info.IsDir() {
		return t.createErrorResponse(
			fmt.Errorf("%s is not a directory", absPath),
			fmt.Sprintf("%s is not a directory", absPath),
		)
	}

	// Change directory
	err = os.Chdir(absPath)
	if err != nil {
		return t.createErrorResponse(err, fmt.Sprintf("Failed to change directory to %s: %s", absPath, err.Error()))
	}

	// Create success response
	type ChangeDirectoryResponse struct {
		Success bool   `json:"success"`
		OldPath string `json:"oldPath,omitempty"`
		NewPath string `json:"newPath"`
		Message string `json:"message"`
	}

	response := ChangeDirectoryResponse{
		Success: true,
		NewPath: absPath,
		Message: fmt.Sprintf("Successfully changed directory to %s", absPath),
	}

	// Convert to JSON
	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return t.createErrorResponse(err, fmt.Sprintf("Failed to marshal JSON response: %s", err.Error()))
	}

	return string(jsonData), nil
}
