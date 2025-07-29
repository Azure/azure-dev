package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tmc/langchaingo/callbacks"
)

// ChangeDirectoryTool implements the Tool interface for changing the current working directory
type ChangeDirectoryTool struct {
	CallbacksHandler callbacks.Handler
}

func (t ChangeDirectoryTool) Name() string {
	return "change_directory"
}

func (t ChangeDirectoryTool) Description() string {
	return "Change the current working directory. Input: directory path (e.g., '../parent' or './subfolder' or absolute path)"
}

func (t ChangeDirectoryTool) Call(ctx context.Context, input string) (string, error) {
	// Invoke callback for tool start
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolStart(ctx, fmt.Sprintf("change_directory: %s", input))
	}

	if input == "" {
		err := fmt.Errorf("directory path is required")
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, err)
		}
		return "", err
	}

	// Get current directory for reference
	currentDir, _ := os.Getwd()

	// Convert to absolute path
	absPath, err := filepath.Abs(input)
	if err != nil {
		toolErr := fmt.Errorf("failed to resolve path %s: %w", input, err)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}

	// Check if directory exists
	info, err := os.Stat(absPath)
	if err != nil {
		toolErr := fmt.Errorf("directory %s does not exist: %w", absPath, err)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}
	if !info.IsDir() {
		toolErr := fmt.Errorf("%s is not a directory", absPath)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}

	// Change directory
	err = os.Chdir(absPath)
	if err != nil {
		toolErr := fmt.Errorf("failed to change directory to %s: %w", absPath, err)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}

	output := fmt.Sprintf("Changed directory from %s to %s", currentDir, absPath)

	// Invoke callback for tool end
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolEnd(ctx, output)
	}

	return output, nil
}
