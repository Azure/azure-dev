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

// ChangeDirectoryTool implements the Tool interface for changing the current working directory
type ChangeDirectoryTool struct {
	common.BuiltInTool
	securityManager *security.Manager
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

func (t ChangeDirectoryTool) Call(ctx context.Context, input string) (string, error) {
	input = strings.TrimSpace(input)
	input = strings.Trim(input, `"`)

	if input == "" {
		return common.CreateErrorResponse(fmt.Errorf("directory path is required"), "Directory path is required")
	}

	// Security validation for directory changes (more restrictive)
	validatedPath, err := t.securityManager.ValidatePath(input)
	if err != nil {
		return common.CreateErrorResponse(
			err,
			"Access denied: directory change operation not permitted outside the allowed directory",
		)
	}

	// Check if directory exists
	info, err := os.Stat(validatedPath)
	if err != nil {
		return common.CreateErrorResponse(err, fmt.Sprintf("Directory %s does not exist: %s", validatedPath, err.Error()))
	}
	if !info.IsDir() {
		return common.CreateErrorResponse(
			fmt.Errorf("%s is not a directory", validatedPath),
			fmt.Sprintf("%s is not a directory", validatedPath),
		)
	}

	// Get current directory before changing (for response)
	oldDir, _ := os.Getwd()

	// Change directory
	err = os.Chdir(validatedPath)
	if err != nil {
		return common.CreateErrorResponse(
			err,
			fmt.Sprintf("Failed to change directory to %s: %s", validatedPath, err.Error()),
		)
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
		OldPath: oldDir,
		NewPath: validatedPath,
		Message: fmt.Sprintf("Successfully changed directory to %s", validatedPath),
	}

	// Convert to JSON
	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return common.CreateErrorResponse(err, fmt.Sprintf("Failed to marshal JSON response: %s", err.Error()))
	}

	return string(jsonData), nil
}
