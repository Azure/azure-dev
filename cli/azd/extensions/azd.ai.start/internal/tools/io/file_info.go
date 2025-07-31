package io

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tmc/langchaingo/callbacks"
)

// FileInfoTool implements the Tool interface for getting file information
type FileInfoTool struct {
	CallbacksHandler callbacks.Handler
}

func (t FileInfoTool) Name() string {
	return "file_info"
}

func (t FileInfoTool) Description() string {
	return "Get information about a file (size, modification time, permissions). Input: file path (e.g., 'data.txt' or './docs/readme.md'). Returns JSON with file information."
}

func (t FileInfoTool) Call(ctx context.Context, input string) (string, error) {
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolStart(ctx, fmt.Sprintf("file_info: %s", input))
	}

	input = strings.TrimPrefix(input, `"`)
	input = strings.TrimSuffix(input, `"`)

	if input == "" {
		err := fmt.Errorf("file path is required")
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, err)
		}
		return "", err
	}

	info, err := os.Stat(input)
	if err != nil {
		toolErr := fmt.Errorf("failed to get info for %s: %w", input, err)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}

	// Prepare JSON response structure
	type FileInfoResponse struct {
		Path         string    `json:"path"`
		Name         string    `json:"name"`
		Type         string    `json:"type"`
		IsDirectory  bool      `json:"isDirectory"`
		Size         int64     `json:"size"`
		ModifiedTime time.Time `json:"modifiedTime"`
		Permissions  string    `json:"permissions"`
	}

	var fileType string
	if info.IsDir() {
		fileType = "directory"
	} else {
		fileType = "file"
	}

	response := FileInfoResponse{
		Path:         input,
		Name:         info.Name(),
		Type:         fileType,
		IsDirectory:  info.IsDir(),
		Size:         info.Size(),
		ModifiedTime: info.ModTime(),
		Permissions:  info.Mode().String(),
	}

	// Convert to JSON
	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		toolErr := fmt.Errorf("failed to marshal JSON response: %w", err)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}

	output := string(jsonData)

	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolEnd(ctx, output)
	}

	return output, nil
}
