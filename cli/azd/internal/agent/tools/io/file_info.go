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

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
)

// FileInfoTool implements the Tool interface for getting file information
type FileInfoTool struct{}

func (t FileInfoTool) Name() string {
	return "file_info"
}

func (t FileInfoTool) Description() string {
	return "Get information about a file (size, modification time, permissions). " +
		"Input: file path (e.g., 'data.txt' or './docs/readme.md'). Returns JSON with file information."
}

// createErrorResponse creates a JSON error response
func (t FileInfoTool) createErrorResponse(err error, message string) (string, error) {
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

func (t FileInfoTool) Call(ctx context.Context, input string) (string, error) {
	input = strings.TrimPrefix(input, `"`)
	input = strings.TrimSuffix(input, `"`)
	input = strings.TrimSpace(input)

	if input == "" {
		return t.createErrorResponse(fmt.Errorf("file path is required"), "File path is required")
	}

	info, err := os.Stat(input)
	if err != nil {
		if os.IsNotExist(err) {
			return t.createErrorResponse(err, fmt.Sprintf("File or directory %s does not exist", input))
		}
		return t.createErrorResponse(err, fmt.Sprintf("Failed to get info for %s: %s", input, err.Error()))
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
		Path:         input,
		Name:         info.Name(),
		Type:         fileType,
		IsDirectory:  info.IsDir(),
		Size:         info.Size(),
		ModifiedTime: info.ModTime(),
		Permissions:  info.Mode().String(),
		Message:      fmt.Sprintf("Successfully retrieved information for %s", input),
	}

	// Convert to JSON
	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return t.createErrorResponse(err, fmt.Sprintf("Failed to marshal JSON response: %s", err.Error()))
	}

	return string(jsonData), nil
}
