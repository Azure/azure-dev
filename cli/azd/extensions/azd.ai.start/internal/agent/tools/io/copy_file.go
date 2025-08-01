package io

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"azd.ai.start/internal/agent/tools/common"
	"github.com/tmc/langchaingo/callbacks"
)

// CopyFileTool implements the Tool interface for copying files
type CopyFileTool struct {
	CallbacksHandler callbacks.Handler
}

func (t CopyFileTool) Name() string {
	return "copy_file"
}

func (t CopyFileTool) Description() string {
	return `Copy a file to a new location. 
Input: JSON object with required 'source' and 'destination' fields: {"source": "file.txt", "destination": "backup.txt"}
Returns: JSON with copy operation details or error information.
The input must be formatted as a single line valid JSON string.`
}

func (t CopyFileTool) Call(ctx context.Context, input string) (string, error) {
	// Parse JSON input
	type InputParams struct {
		Source      string `json:"source"`
		Destination string `json:"destination"`
	}

	var params InputParams

	// Clean the input first
	cleanInput := strings.TrimSpace(input)

	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolStart(ctx, fmt.Sprintf("copy_file: %s", cleanInput))
	}

	// Parse as JSON - this is now required
	if err := json.Unmarshal([]byte(cleanInput), &params); err != nil {
		errorResponse := common.ErrorResponse{
			Error:   true,
			Message: fmt.Sprintf("Invalid JSON input: %s. Expected format: {\"source\": \"file.txt\", \"destination\": \"backup.txt\"}", err.Error()),
		}
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, fmt.Errorf("failed to parse JSON input: %w", err))
		}
		jsonData, _ := json.MarshalIndent(errorResponse, "", "  ")
		return string(jsonData), nil
	}

	source := strings.TrimSpace(params.Source)
	destination := strings.TrimSpace(params.Destination)

	if source == "" || destination == "" {
		errorResponse := common.ErrorResponse{
			Error:   true,
			Message: "Both source and destination paths are required",
		}
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, fmt.Errorf("both source and destination paths are required"))
		}
		jsonData, _ := json.MarshalIndent(errorResponse, "", "  ")
		return string(jsonData), nil
	}

	// Check if source file exists
	sourceInfo, err := os.Stat(source)
	if err != nil {
		errorResponse := common.ErrorResponse{
			Error:   true,
			Message: fmt.Sprintf("Source file %s does not exist: %s", source, err.Error()),
		}
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, fmt.Errorf("source file %s does not exist: %w", source, err))
		}
		jsonData, _ := json.MarshalIndent(errorResponse, "", "  ")
		return string(jsonData), nil
	}

	if sourceInfo.IsDir() {
		errorResponse := common.ErrorResponse{
			Error:   true,
			Message: fmt.Sprintf("Source %s is a directory. Use copy_directory for directories", source),
		}
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, fmt.Errorf("source %s is a directory. Use copy_directory for directories", source))
		}
		jsonData, _ := json.MarshalIndent(errorResponse, "", "  ")
		return string(jsonData), nil
	}

	// Open source file
	sourceFile, err := os.Open(source)
	if err != nil {
		errorResponse := common.ErrorResponse{
			Error:   true,
			Message: fmt.Sprintf("Failed to open source file %s: %s", source, err.Error()),
		}
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, fmt.Errorf("failed to open source file %s: %w", source, err))
		}
		jsonData, _ := json.MarshalIndent(errorResponse, "", "  ")
		return string(jsonData), nil
	}
	defer sourceFile.Close()

	// Create destination file
	destFile, err := os.Create(destination)
	if err != nil {
		errorResponse := common.ErrorResponse{
			Error:   true,
			Message: fmt.Sprintf("Failed to create destination file %s: %s", destination, err.Error()),
		}
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, fmt.Errorf("failed to create destination file %s: %w", destination, err))
		}
		jsonData, _ := json.MarshalIndent(errorResponse, "", "  ")
		return string(jsonData), nil
	}
	defer destFile.Close()

	// Copy contents
	bytesWritten, err := io.Copy(destFile, sourceFile)
	if err != nil {
		errorResponse := common.ErrorResponse{
			Error:   true,
			Message: fmt.Sprintf("Failed to copy file: %s", err.Error()),
		}
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, fmt.Errorf("failed to copy file: %w", err))
		}
		jsonData, _ := json.MarshalIndent(errorResponse, "", "  ")
		return string(jsonData), nil
	}

	// Prepare JSON response structure
	type CopyResponse struct {
		Success     bool   `json:"success"`
		Source      string `json:"source"`
		Destination string `json:"destination"`
		BytesCopied int64  `json:"bytesCopied"`
		Message     string `json:"message"`
	}

	response := CopyResponse{
		Success:     true,
		Source:      source,
		Destination: destination,
		BytesCopied: bytesWritten,
		Message:     fmt.Sprintf("Successfully copied %s to %s (%d bytes)", source, destination, bytesWritten),
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
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolEnd(ctx, fmt.Sprintf("Copied %s to %s (%d bytes)", source, destination, bytesWritten))
	}

	return output, nil
}
