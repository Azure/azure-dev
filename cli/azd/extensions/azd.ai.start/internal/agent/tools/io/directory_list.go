package io

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"azd.ai.start/internal/agent/tools/common"
	"github.com/tmc/langchaingo/callbacks"
)

// DirectoryListTool implements the Tool interface for listing directory contents
type DirectoryListTool struct {
	CallbacksHandler callbacks.Handler
}

func (t DirectoryListTool) Name() string {
	return "list_directory"
}

func (t DirectoryListTool) Description() string {
	return `List files and folders in a directory. 
Input: JSON object with required 'path' field: {"path": ".", "includeHidden": false}
Returns: JSON with directory contents including file names, types, and sizes.
The input must be formatted as a single line valid JSON string.`
}

func (t DirectoryListTool) Call(ctx context.Context, input string) (string, error) {
	// Parse JSON input
	type InputParams struct {
		Path          string `json:"path"`
		IncludeHidden bool   `json:"includeHidden,omitempty"`
	}

	var params InputParams

	// Clean the input first
	cleanInput := strings.TrimSpace(input)

	// Parse as JSON - this is now required
	if err := json.Unmarshal([]byte(cleanInput), &params); err != nil {
		errorResponse := common.ErrorResponse{
			Error:   true,
			Message: fmt.Sprintf("Invalid JSON input: %s. Expected format: {\"path\": \".\", \"include_hidden\": false}", err.Error()),
		}
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, fmt.Errorf("failed to parse JSON input: %w", err))
		}
		jsonData, _ := json.MarshalIndent(errorResponse, "", "  ")
		return string(jsonData), nil
	}

	// Validate required path field
	if params.Path == "" {
		params.Path = "."
	}

	path := strings.TrimSpace(params.Path)

	// Add debug logging
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolStart(ctx, fmt.Sprintf("Processing JSON input: path='%s', include_hidden=%v", path, params.IncludeHidden))
	}

	// Get absolute path for clarity - handle "." explicitly to avoid potential issues
	var absPath string
	var err error

	if path == "." {
		// Explicitly get current working directory instead of relying on filepath.Abs(".")
		absPath, err = os.Getwd()
		if err != nil {
			errorResponse := common.ErrorResponse{
				Error:   true,
				Message: fmt.Sprintf("Failed to get current working directory: %s", err.Error()),
			}
			if t.CallbacksHandler != nil {
				t.CallbacksHandler.HandleToolError(ctx, fmt.Errorf("failed to get current working directory: %w", err))
			}
			jsonData, _ := json.MarshalIndent(errorResponse, "", "  ")
			return string(jsonData), nil
		}
	} else {
		absPath, err = filepath.Abs(path)
		if err != nil {
			errorResponse := common.ErrorResponse{
				Error:   true,
				Message: fmt.Sprintf("Failed to get absolute path for %s: %s", path, err.Error()),
			}
			if t.CallbacksHandler != nil {
				t.CallbacksHandler.HandleToolError(ctx, fmt.Errorf("failed to get absolute path for %s: %w", path, err))
			}
			jsonData, _ := json.MarshalIndent(errorResponse, "", "  ")
			return string(jsonData), nil
		}
	}

	// Invoke callback for tool execution start
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolStart(ctx, fmt.Sprintf("Reading directory %s (absolute: %s)", path, absPath))
	}

	// Check if directory exists
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolStart(ctx, fmt.Sprintf("Checking if directory exists: '%s'", absPath))
	}

	info, err := os.Stat(absPath)
	if err != nil {
		var message string
		if os.IsNotExist(err) {
			message = fmt.Sprintf("Directory does not exist: %s", absPath)
		} else {
			message = fmt.Sprintf("Failed to access %s: %s (original input: '%s', cleaned path: '%s')", absPath, err.Error(), input, path)
		}

		errorResponse := common.ErrorResponse{
			Error:   true,
			Message: message,
		}
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, fmt.Errorf("failed to access %s: %w", absPath, err))
		}
		jsonData, _ := json.MarshalIndent(errorResponse, "", "  ")
		return string(jsonData), nil
	}

	if !info.IsDir() {
		errorResponse := common.ErrorResponse{
			Error:   true,
			Message: fmt.Sprintf("Path is not a directory: %s", absPath),
		}
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, fmt.Errorf("%s is not a directory", absPath))
		}
		jsonData, _ := json.MarshalIndent(errorResponse, "", "  ")
		return string(jsonData), nil
	}

	// List directory contents
	files, err := os.ReadDir(absPath)
	if err != nil {
		errorResponse := common.ErrorResponse{
			Error:   true,
			Message: fmt.Sprintf("Failed to read directory %s: %s", absPath, err.Error()),
		}
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, fmt.Errorf("failed to read directory %s: %w", absPath, err))
		}
		jsonData, _ := json.MarshalIndent(errorResponse, "", "  ")
		return string(jsonData), nil
	}

	// Prepare JSON response structure
	type FileInfo struct {
		Name  string `json:"name"`
		Type  string `json:"type"`
		Size  int64  `json:"size,omitempty"`
		IsDir bool   `json:"isDirectory"`
	}

	type DirectoryResponse struct {
		Path       string     `json:"path"`
		TotalItems int        `json:"totalItems"`
		Items      []FileInfo `json:"items"`
	}

	var items []FileInfo

	for _, file := range files {
		fileInfo := FileInfo{
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

	response := DirectoryResponse{
		Path:       absPath,
		TotalItems: len(files),
		Items:      items,
	}

	// Convert to JSON
	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		errorResponse := common.ErrorResponse{
			Error:   true,
			Message: fmt.Sprintf("Failed to marshal JSON response: %s", err.Error()),
		}
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, fmt.Errorf("failed to marshal JSON response: %w", err))
		}
		errorJsonData, _ := json.MarshalIndent(errorResponse, "", "  ")
		return string(errorJsonData), nil
	}

	output := string(jsonData)

	// Invoke callback for tool end
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolEnd(ctx, "")
	}

	return output, nil
}
