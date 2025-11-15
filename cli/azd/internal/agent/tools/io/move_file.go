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

// MoveFileTool implements the Tool interface for moving/renaming files
type MoveFileTool struct {
	common.BuiltInTool
	securityManager *security.Manager
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

func (t MoveFileTool) Call(ctx context.Context, input string) (string, error) {
	input = strings.TrimPrefix(input, `"`)
	input = strings.TrimSuffix(input, `"`)
	input = strings.TrimSpace(input)

	if input == "" {
		return common.CreateErrorResponse(
			fmt.Errorf("input is required in format 'source|destination'"),
			"Input is required in format 'source|destination'",
		)
	}

	// Split on first occurrence of '|' to separate source from destination
	parts := strings.SplitN(input, "|", 2)
	if len(parts) != 2 {
		return common.CreateErrorResponse(
			fmt.Errorf("invalid input format"),
			"Invalid input format. Use 'source|destination'",
		)
	}

	source := strings.TrimSpace(parts[0])
	destination := strings.TrimSpace(parts[1])

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
	} // Check if source exists
	sourceInfo, err := os.Stat(validatedSource)
	if err != nil {
		if os.IsNotExist(err) {
			return common.CreateErrorResponse(err, fmt.Sprintf("Source %s does not exist", validatedSource))
		}
		return common.CreateErrorResponse(err, fmt.Sprintf("Cannot access source %s: %s", validatedSource, err.Error()))
	}

	// Check if destination already exists
	if _, err := os.Stat(validatedDest); err == nil {
		return common.CreateErrorResponse(
			fmt.Errorf("destination %s already exists", validatedDest),
			fmt.Sprintf("Destination %s already exists", validatedDest),
		)
	}

	// Move/rename the file
	err = os.Rename(validatedSource, validatedDest)
	if err != nil {
		return common.CreateErrorResponse(
			err,
			fmt.Sprintf("Failed to move %s to %s: %s", validatedSource, validatedDest, err.Error()),
		)
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
		Source:      validatedSource,
		Destination: validatedDest,
		Type:        fileType,
		Size:        sourceInfo.Size(),
		Message: fmt.Sprintf(
			"Successfully moved %s from %s to %s (%d bytes)",
			fileType,
			validatedSource,
			validatedDest,
			sourceInfo.Size(),
		),
	}

	// Convert to JSON
	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return common.CreateErrorResponse(err, fmt.Sprintf("Failed to marshal JSON response: %s", err.Error()))
	}

	return string(jsonData), nil
}
