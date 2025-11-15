// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package io

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/azure/azure-dev/internal/agent/tools/common"
	"github.com/mark3labs/mcp-go/mcp"
)

// CurrentDirectoryTool implements the Tool interface for getting current directory
type CurrentDirectoryTool struct {
	common.BuiltInTool
}

func (t CurrentDirectoryTool) Name() string {
	return "current_directory"
}

func (t CurrentDirectoryTool) Annotations() mcp.ToolAnnotation {
	return mcp.ToolAnnotation{
		Title:           "Get Current Directory",
		ReadOnlyHint:    common.ToPtr(true),
		DestructiveHint: common.ToPtr(false),
		IdempotentHint:  common.ToPtr(true),
		OpenWorldHint:   common.ToPtr(false),
	}
}

func (t CurrentDirectoryTool) Description() string {
	return "Get the current working directory for the project workspace " +
		"Input: use 'current' or '.' (any input works)"
}

func (t CurrentDirectoryTool) Call(ctx context.Context, input string) (string, error) {
	currentDir, err := os.Getwd()
	if err != nil {
		return common.CreateErrorResponse(err, fmt.Sprintf("Failed to get current directory: %s", err.Error()))
	}

	// Create success response
	type CurrentDirectoryResponse struct {
		Success          bool   `json:"success"`
		CurrentDirectory string `json:"currentDirectory"`
		Message          string `json:"message"`
	}

	response := CurrentDirectoryResponse{
		Success:          true,
		CurrentDirectory: currentDir,
		Message:          fmt.Sprintf("Current directory is %s", currentDir),
	}

	// Convert to JSON
	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return common.CreateErrorResponse(err, fmt.Sprintf("Failed to marshal JSON response: %s", err.Error()))
	}

	return string(jsonData), nil
}
