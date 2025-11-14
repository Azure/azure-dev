// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package io

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/azure/azure-dev/internal/agent/security"
	"github.com/azure/azure-dev/internal/agent/tools/common"
	"github.com/mark3labs/mcp-go/mcp"
)

// FileInfoTool implements the Tool interface for getting file information
type FileInfoTool struct {
	common.BuiltInTool
	securityManager *security.Manager
}

func (t FileInfoTool) Name() string {
	return "file_info"
}

func (t FileInfoTool) Annotations() mcp.ToolAnnotation {
	return mcp.ToolAnnotation{
		Title:           "Get File Information",
		ReadOnlyHint:    common.ToPtr(true),
		DestructiveHint: common.ToPtr(false),
		IdempotentHint:  common.ToPtr(true),
		OpenWorldHint:   common.ToPtr(false),
	}
}

func (t FileInfoTool) Description() string {
	return "Get information about a file (size, modification time, permissions). " +
		"Input: file path (e.g., 'data.txt' or './docs/readme.md'). Returns JSON with file information."
}

func (t FileInfoTool) Call(ctx context.Context, input string) (string, error) {
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
			"Access denied: file info operation not permitted outside the allowed directory",
		)
	}

	info, err := os.Stat(validatedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return common.CreateErrorResponse(err, fmt.Sprintf("File or directory %s does not exist", validatedPath))
		}
		return common.CreateErrorResponse(err, fmt.Sprintf("Failed to get info for %s: %s", validatedPath, err.Error()))
	}

	// Prepare JSON response structure
	type FileInfoResponse struct {
		Success      bool      `json:"success"`
		Path         string    `json:"path"`
		Name         string    `json:"name"`
		Type         string    `json:"type"`
		IsDirectory  bool      `json:"isDirectory"`
		Size         int64     `json:"size"`
		ModifiedTime time.Time `json:"modifiedTime"`
		Permissions  string    `json:"permissions"`
		Message      string    `json:"message"`
	}

	var fileType string
	if info.IsDir() {
		fileType = "directory"
	} else {
		fileType = "file"
	}

	response := FileInfoResponse{
		Success:      true,
		Path:         validatedPath,
		Name:         info.Name(),
		Type:         fileType,
		IsDirectory:  info.IsDir(),
		Size:         info.Size(),
		ModifiedTime: info.ModTime(),
		Permissions:  info.Mode().String(),
		Message:      fmt.Sprintf("Successfully retrieved information for %s", validatedPath),
	}

	// Convert to JSON
	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return common.CreateErrorResponse(err, fmt.Sprintf("Failed to marshal JSON response: %s", err.Error()))
	}

	return string(jsonData), nil
}
