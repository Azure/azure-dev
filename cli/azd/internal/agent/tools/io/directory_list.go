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

// createErrorResponse creates a JSON error response
func (t DirectoryListTool) createErrorResponse(err error, message string) (string, error) {
	return common.CreateErrorResponse(err, message)
}

func (t DirectoryListTool) Call(ctx context.Context, input string) (string, error) {
	var params DirectoryListRequest

	// Clean the input first
	cleanInput := strings.TrimSpace(input)

	// Parse as JSON - this is now required
	if err := json.Unmarshal([]byte(cleanInput), &params); err != nil {
		return t.createErrorResponse(
			err,
			fmt.Sprintf("Invalid JSON input: %s. Expected format: {\"path\": \".\", \"includeHidden\": false}", err.Error()),
		)
	}

	// Validate required path field
	if params.Path == "" {
		params.Path = "."
	}

	path := strings.TrimSpace(params.Path)

	// Get absolute path for clarity - handle "." explicitly to avoid potential issues
	var absPath string
	var err error

	if path == "." {
		// Explicitly get current working directory instead of relying on filepath.Abs(".")
		absPath, err = os.Getwd()
		if err != nil {
			return t.createErrorResponse(err, fmt.Sprintf("Failed to get current working directory: %s", err.Error()))
		}
	} else {
		absPath, err = filepath.Abs(path)
		if err != nil {
			return t.createErrorResponse(err, fmt.Sprintf("Failed to get absolute path for %s: %s", path, err.Error()))
		}
	}

	// Check if directory exists and is accessible
	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return t.createErrorResponse(err, fmt.Sprintf("Directory %s does not exist", absPath))
		}
		return t.createErrorResponse(err, fmt.Sprintf("Failed to access %s: %s", absPath, err.Error()))
	}

	if !info.IsDir() {
		return t.createErrorResponse(
			fmt.Errorf("%s is not a directory", absPath),
			fmt.Sprintf("%s is not a directory", absPath),
		)
	}

	// Read directory contents
	files, err := os.ReadDir(absPath)
	if err != nil {
		return t.createErrorResponse(err, fmt.Sprintf("Failed to read directory %s: %s", absPath, err.Error()))
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
		Path:       absPath,
		TotalItems: len(items),
		Items:      items,
		Message:    fmt.Sprintf("Successfully listed %d items in directory %s", len(items), absPath),
	}

	// Convert to JSON
	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return t.createErrorResponse(err, fmt.Sprintf("Failed to marshal JSON response: %s", err.Error()))
	}

	return string(jsonData), nil
}
