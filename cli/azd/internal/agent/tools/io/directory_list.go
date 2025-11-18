// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package io

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/agent/security"
	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
	"github.com/mark3labs/mcp-go/mcp"
)

// DirectoryListRequest represents the JSON payload for directory listing requests
type DirectoryListRequest struct {
	Path          string `json:"path"`
	IncludeHidden bool   `json:"includeHidden,omitempty"`
}

// DirectoryListFileInfo represents file information in directory listings
type DirectoryListFileInfo struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Size  int64  `json:"size,omitempty"`
	IsDir bool   `json:"isDirectory"`
}

// DirectoryListResponse represents the JSON output for the list_directory tool
type DirectoryListResponse struct {
	Success    bool                    `json:"success"`
	Path       string                  `json:"path"`
	TotalItems int                     `json:"totalItems"`
	Items      []DirectoryListFileInfo `json:"items"`
	Message    string                  `json:"message"`
}

// DirectoryListTool implements the Tool interface for listing directory contents
type DirectoryListTool struct {
	common.BuiltInTool
	securityManager *security.Manager
}

func (t DirectoryListTool) Name() string {
	return "list_directory"
}

func (t DirectoryListTool) Annotations() mcp.ToolAnnotation {
	return mcp.ToolAnnotation{
		Title:           "List Directory Contents",
		ReadOnlyHint:    common.ToPtr(true),
		DestructiveHint: common.ToPtr(false),
		IdempotentHint:  common.ToPtr(true),
		OpenWorldHint:   common.ToPtr(false),
	}
}

func (t DirectoryListTool) Description() string {
	return `List files and folders in a directory. 
Input: JSON object with required 'path' field: {"path": ".", "includeHidden": false}
Returns: JSON with directory contents including file names, types, and sizes.
The input must be formatted as a single line valid JSON string.`
}

func (t DirectoryListTool) Call(ctx context.Context, input string) (string, error) {
	var params DirectoryListRequest

	// Clean the input first
	cleanInput := strings.TrimSpace(input)

	// Parse as JSON - this is now required
	if err := json.Unmarshal([]byte(cleanInput), &params); err != nil {
		return common.CreateErrorResponse(
			err,
			fmt.Sprintf("Invalid JSON input: %s. Expected format: {\"path\": \".\", \"includeHidden\": false}", err.Error()),
		)
	}

	// Validate required path field
	if params.Path == "" {
		params.Path = "."
	}

	path := strings.TrimSpace(params.Path)

	// Security validation
	validatedPath, err := t.securityManager.ValidatePath(path)
	if err != nil {
		return common.CreateErrorResponse(
			err,
			"Access denied: directory listing operation not permitted outside the allowed directory",
		)
	}

	// Check if directory exists
	info, err := os.Stat(validatedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return common.CreateErrorResponse(err, fmt.Sprintf("Directory %s does not exist", validatedPath))
		}
		return common.CreateErrorResponse(err, fmt.Sprintf("Failed to access %s: %s", validatedPath, err.Error()))
	}

	if !info.IsDir() {
		return common.CreateErrorResponse(
			fmt.Errorf("%s is not a directory", validatedPath),
			fmt.Sprintf("%s is not a directory", validatedPath),
		)
	}

	// Read directory contents
	files, err := os.ReadDir(validatedPath)
	if err != nil {
		return common.CreateErrorResponse(err, fmt.Sprintf("Failed to read directory %s: %s", validatedPath, err.Error()))
	}

	// Prepare JSON response structure
	var items []DirectoryListFileInfo

	for _, file := range files {
		// Skip hidden files if not requested
		if !params.IncludeHidden && strings.HasPrefix(file.Name(), ".") {
			continue
		}

		fileInfo := DirectoryListFileInfo{
			Name:  file.Name(),
			IsDir: file.IsDir(),
		}

		if file.IsDir() {
			fileInfo.Type = "directory"
		} else {
			fileInfo.Type = "file"
			if info, err := file.Info(); err == nil {
				fileInfo.Size = info.Size()
			}
		}

		items = append(items, fileInfo)
	}

	response := DirectoryListResponse{
		Success:    true,
		Path:       validatedPath,
		TotalItems: len(items),
		Items:      items,
		Message:    fmt.Sprintf("Successfully listed %d items in directory %s", len(items), validatedPath),
	}

	// Convert to JSON
	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return common.CreateErrorResponse(err, fmt.Sprintf("Failed to marshal JSON response: %s", err.Error()))
	}

	return string(jsonData), nil
}
