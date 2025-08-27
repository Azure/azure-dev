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

// CreateDirectoryTool implements the Tool interface for creating directories
type CreateDirectoryTool struct {
	common.BuiltInTool
}

func (t CreateDirectoryTool) Name() string {
	return "create_directory"
}

func (t CreateDirectoryTool) Annotations() mcp.ToolAnnotation {
	return mcp.ToolAnnotation{
		Title:           "Create Directory",
		ReadOnlyHint:    common.ToPtr(false),
		DestructiveHint: common.ToPtr(false),
		IdempotentHint:  common.ToPtr(true),
		OpenWorldHint:   common.ToPtr(false),
	}
}

func (t CreateDirectoryTool) Description() string {
	return "Create a directory (and any necessary parent directories). " +
		"Input: directory path (e.g., 'docs' or './src/components')"
}

func (t CreateDirectoryTool) Call(ctx context.Context, input string) (string, error) {
	input = strings.TrimPrefix(input, `"`)
	input = strings.TrimSuffix(input, `"`)
	input = strings.TrimSpace(input)

	if input == "" {
		return common.CreateErrorResponse(fmt.Errorf("directory path is required"), "Directory path is required")
	}

	err := os.MkdirAll(input, 0755)
	if err != nil {
		return common.CreateErrorResponse(err, fmt.Sprintf("Failed to create directory %s: %s", input, err.Error()))
	}

	// Check if directory already existed or was newly created
	info, err := os.Stat(input)
	if err != nil {
		return common.CreateErrorResponse(err, fmt.Sprintf("Failed to verify directory creation: %s", err.Error()))
	}

	if !info.IsDir() {
		return common.CreateErrorResponse(
			fmt.Errorf("%s exists but is not a directory", input),
			fmt.Sprintf("%s exists but is not a directory", input),
		)
	}

	// Create success response
	type CreateDirectoryResponse struct {
		Success bool   `json:"success"`
		Path    string `json:"path"`
		Message string `json:"message"`
	}

	response := CreateDirectoryResponse{
		Success: true,
		Path:    input,
		Message: fmt.Sprintf("Successfully created directory: %s", input),
	}

	// Convert to JSON
	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return common.CreateErrorResponse(err, fmt.Sprintf("Failed to marshal JSON response: %s", err.Error()))
	}

	return string(jsonData), nil
}
