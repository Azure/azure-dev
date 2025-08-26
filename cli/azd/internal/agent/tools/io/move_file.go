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

// MoveFileTool implements the Tool interface for moving/renaming files
type MoveFileTool struct {
	common.BuiltInTool
}

func (t MoveFileTool) Name() string {
	return "move_file"
}

func (t MoveFileTool) Annotations() mcp.ToolAnnotation {
	return mcp.ToolAnnotation{
		Title:           "Move or Rename File",
		ReadOnlyHint:    common.ToPtr(false),
		DestructiveHint: common.ToPtr(true),
		IdempotentHint:  common.ToPtr(false),
		OpenWorldHint:   common.ToPtr(false),
	}
}

func (t MoveFileTool) Description() string {
	return "Move or rename a file.\n" +
		"Input format: 'source|destination' (e.g., 'old.txt|new.txt' or './file.txt|./folder/file.txt')"
}

// createErrorResponse creates a JSON error response
func (t MoveFileTool) createErrorResponse(err error, message string) (string, error) {
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

func (t MoveFileTool) Call(ctx context.Context, input string) (string, error) {
	input = strings.TrimPrefix(input, `"`)
	input = strings.TrimSuffix(input, `"`)
	input = strings.TrimSpace(input)

	if input == "" {
		return t.createErrorResponse(
			fmt.Errorf("input is required in format 'source|destination'"),
			"Input is required in format 'source|destination'",
		)
	}

	// Split on first occurrence of '|' to separate source from destination
	parts := strings.SplitN(input, "|", 2)
	if len(parts) != 2 {
		return t.createErrorResponse(fmt.Errorf("invalid input format"), "Invalid input format. Use 'source|destination'")
	}

	source := strings.TrimSpace(parts[0])
	destination := strings.TrimSpace(parts[1])

	if source == "" || destination == "" {
		return t.createErrorResponse(
			fmt.Errorf("both source and destination paths are required"),
			"Both source and destination paths are required",
		)
	}

	// Check if source exists
	sourceInfo, err := os.Stat(source)
	if err != nil {
		if os.IsNotExist(err) {
			return t.createErrorResponse(err, fmt.Sprintf("Source %s does not exist", source))
		}
		return t.createErrorResponse(err, fmt.Sprintf("Cannot access source %s: %s", source, err.Error()))
	}

	// Check if destination already exists
	if _, err := os.Stat(destination); err == nil {
		return t.createErrorResponse(
			fmt.Errorf("destination %s already exists", destination),
			fmt.Sprintf("Destination %s already exists", destination),
		)
	}

	// Move/rename the file
	err = os.Rename(source, destination)
	if err != nil {
		return t.createErrorResponse(err, fmt.Sprintf("Failed to move %s to %s: %s", source, destination, err.Error()))
	}

	// Create success response
	type MoveFileResponse struct {
		Success     bool   `json:"success"`
		Source      string `json:"source"`
		Destination string `json:"destination"`
		Type        string `json:"type"`
		Size        int64  `json:"size"`
		Message     string `json:"message"`
	}

	fileType := "file"
	if sourceInfo.IsDir() {
		fileType = "directory"
	}

	response := MoveFileResponse{
		Success:     true,
		Source:      source,
		Destination: destination,
		Type:        fileType,
		Size:        sourceInfo.Size(),
		Message: fmt.Sprintf(
			"Successfully moved %s from %s to %s (%d bytes)",
			fileType,
			source,
			destination,
			sourceInfo.Size(),
		),
	}

	// Convert to JSON
	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return t.createErrorResponse(err, fmt.Sprintf("Failed to marshal JSON response: %s", err.Error()))
	}

	return string(jsonData), nil
}
