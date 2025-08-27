// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package io

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
	"github.com/mark3labs/mcp-go/mcp"
)

// DeleteDirectoryTool implements the Tool interface for deleting directories
type DeleteDirectoryTool struct {
	common.BuiltInTool
}

func (t DeleteDirectoryTool) Name() string {
	return "delete_directory"
}

func (t DeleteDirectoryTool) Annotations() mcp.ToolAnnotation {
	return mcp.ToolAnnotation{
		Title:           "Delete Directory",
		ReadOnlyHint:    common.ToPtr(false),
		DestructiveHint: common.ToPtr(true),
		IdempotentHint:  common.ToPtr(false),
		OpenWorldHint:   common.ToPtr(false),
	}
}

func (t DeleteDirectoryTool) Description() string {
	return "Delete a directory and all its contents. Input: directory path (e.g., 'temp-folder' or './old-docs')"
}

func (t DeleteDirectoryTool) Call(ctx context.Context, input string) (string, error) {
	input = strings.TrimPrefix(input, `"`)
	input = strings.TrimSuffix(input, `"`)
	input = strings.TrimSpace(input)

	if input == "" {
		return common.CreateErrorResponse(fmt.Errorf("directory path is required"), "Directory path is required")
	}

	// Check if directory exists
	info, err := os.Stat(input)
	if err != nil {
		if os.IsNotExist(err) {
			return common.CreateErrorResponse(err, fmt.Sprintf("Directory %s does not exist", input))
		}
		return common.CreateErrorResponse(err, fmt.Sprintf("Cannot access directory %s: %s", input, err.Error()))
	}

	// Make sure it's a directory, not a file
	if !info.IsDir() {
		return common.CreateErrorResponse(
			fmt.Errorf("%s is a file, not a directory", input),
			fmt.Sprintf("%s is a file, not a directory. Use delete_file to remove files", input),
		)
	}

	// Count contents before deletion for reporting
	files, err := os.ReadDir(input)
	fileCount := 0
	if err == nil {
		fileCount = len(files)
	}

	// Delete the directory and all contents
	err = os.RemoveAll(input)
	if err != nil {
		return common.CreateErrorResponse(err, fmt.Sprintf("Failed to delete directory %s: %s", input, err.Error()))
	}

	// Create success response
	type DeleteDirectoryResponse struct {
		Success      bool   `json:"success"`
		Path         string `json:"path"`
		ItemsDeleted int    `json:"itemsDeleted"`
		Message      string `json:"message"`
	}

	var message string
	if fileCount > 0 {
		message = fmt.Sprintf("Successfully deleted directory %s (contained %d items)", input, fileCount)
	} else {
		message = fmt.Sprintf("Successfully deleted empty directory %s", input)
	}

	response := DeleteDirectoryResponse{
		Success:      true,
		Path:         input,
		ItemsDeleted: fileCount,
		Message:      message,
	}

	// Convert to JSON
	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return common.CreateErrorResponse(err, fmt.Sprintf("Failed to marshal JSON response: %s", err.Error()))
	}

	return string(jsonData), nil
}
