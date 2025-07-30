package io

import (
	"context"
	"fmt"
	"os"

	"github.com/tmc/langchaingo/callbacks"
)

// DeleteFileTool implements the Tool interface for deleting files
type DeleteFileTool struct {
	CallbacksHandler callbacks.Handler
}

func (t DeleteFileTool) Name() string {
	return "delete_file"
}

func (t DeleteFileTool) Description() string {
	return "Delete a file. Input: file path (e.g., 'temp.txt' or './docs/old-file.md')"
}

func (t DeleteFileTool) Call(ctx context.Context, input string) (string, error) {
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolStart(ctx, fmt.Sprintf("delete_file: %s", input))
	}

	if input == "" {
		err := fmt.Errorf("file path is required")
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, err)
		}
		return "", err
	}

	// Check if file exists and get info
	info, err := os.Stat(input)
	if err != nil {
		toolErr := fmt.Errorf("file %s does not exist: %w", input, err)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}

	// Make sure it's a file, not a directory
	if info.IsDir() {
		err := fmt.Errorf("%s is a directory, not a file. Use delete_directory to remove directories", input)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, err)
		}
		return "", err
	}

	// Delete the file
	err = os.Remove(input)
	if err != nil {
		toolErr := fmt.Errorf("failed to delete file %s: %w", input, err)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}

	output := fmt.Sprintf("Deleted file %s (%d bytes)", input, info.Size())
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolEnd(ctx, output)
	}

	return output, nil
}
