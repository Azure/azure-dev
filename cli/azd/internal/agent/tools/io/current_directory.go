// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package io

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
)

// CurrentDirectoryTool implements the Tool interface for getting current directory
type CurrentDirectoryTool struct{}

func (t CurrentDirectoryTool) Name() string {
	return "cwd"
}

func (t CurrentDirectoryTool) Description() string {
	return "Get the current working directory to understand the project context. " +
		"Input: use 'current' or '.' (any input works)"
}

// createErrorResponse creates a JSON error response
func (t CurrentDirectoryTool) createErrorResponse(err error, message string) (string, error) {
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

func (t CurrentDirectoryTool) Call(ctx context.Context, input string) (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return t.createErrorResponse(err, fmt.Sprintf("Failed to get current directory: %s", err.Error()))
	}

	// Create success response
	type CurrentDirectoryResponse struct {
		Success          bool   `json:"success"`
		CurrentDirectory string `json:"currentDirectory"`
		Message          string `json:"message"`
	}

	response := CurrentDirectoryResponse{
		Success:          true,
		CurrentDirectory: dir,
		Message:          fmt.Sprintf("Current directory is %s", dir),
	}

	// Convert to JSON
	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return t.createErrorResponse(err, fmt.Sprintf("Failed to marshal JSON response: %s", err.Error()))
	}

	return string(jsonData), nil
}
