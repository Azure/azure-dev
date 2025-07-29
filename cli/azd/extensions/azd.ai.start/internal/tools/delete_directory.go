package tools

import (
	"context"
	"fmt"
	"os"

	"github.com/tmc/langchaingo/callbacks"
)

// DeleteDirectoryTool implements the Tool interface for deleting directories
type DeleteDirectoryTool struct {
	CallbacksHandler callbacks.Handler
}

func (t DeleteDirectoryTool) Name() string {
	return "delete_directory"
}

func (t DeleteDirectoryTool) Description() string {
	return "Delete a directory and all its contents. Input: directory path (e.g., 'temp-folder' or './old-docs')"
}

func (t DeleteDirectoryTool) Call(ctx context.Context, input string) (string, error) {
	// Invoke callback for tool start
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolStart(ctx, fmt.Sprintf("delete_directory: %s", input))
	}

	if input == "" {
		err := fmt.Errorf("directory path is required")
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, err)
		}
		return "", err
	}

	// Check if directory exists
	info, err := os.Stat(input)
	if err != nil {
		toolErr := fmt.Errorf("directory %s does not exist: %w", input, err)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}

	// Make sure it's a directory, not a file
	if !info.IsDir() {
		toolErr := fmt.Errorf("%s is a file, not a directory. Use delete_file to remove files", input)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}

	// Count contents before deletion for reporting
	files, err := os.ReadDir(input)
	fileCount := 0
	if err == nil {
		fileCount = len(files)
	}

	// Delete the directory and all contents
	err = os.RemoveAll(input)
	if err != nil {
		toolErr := fmt.Errorf("failed to delete directory %s: %w", input, err)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}

	var output string
	if fileCount > 0 {
		output = fmt.Sprintf("Successfully deleted directory: %s (contained %d items)", input, fileCount)
	} else {
		output = fmt.Sprintf("Successfully deleted empty directory: %s", input)
	}

	// Invoke callback for tool end
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolEnd(ctx, output)
	}

	return output, nil
}
