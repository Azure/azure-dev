package tools

import (
	"context"
	"fmt"
	"os"

	"github.com/tmc/langchaingo/callbacks"
)

// CreateDirectoryTool implements the Tool interface for creating directories
type CreateDirectoryTool struct {
	CallbacksHandler callbacks.Handler
}

func (t CreateDirectoryTool) Name() string {
	return "create_directory"
}

func (t CreateDirectoryTool) Description() string {
	return "Create a directory (and any necessary parent directories). Input: directory path (e.g., 'docs' or './src/components')"
}

func (t CreateDirectoryTool) Call(ctx context.Context, input string) (string, error) {
	// Invoke callback for tool start
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolStart(ctx, fmt.Sprintf("create_directory: %s", input))
	}

	if input == "" {
		err := fmt.Errorf("directory path is required")
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, err)
		}
		return "", err
	}

	err := os.MkdirAll(input, 0755)
	if err != nil {
		toolErr := fmt.Errorf("failed to create directory %s: %w", input, err)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}

	// Check if directory already existed or was newly created
	info, err := os.Stat(input)
	if err != nil {
		toolErr := fmt.Errorf("failed to verify directory creation: %w", err)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}

	if !info.IsDir() {
		toolErr := fmt.Errorf("%s exists but is not a directory", input)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}

	output := fmt.Sprintf("Created directory: %s\n", input)

	// Invoke callback for tool end
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolEnd(ctx, output)
	}

	return output, nil
}
