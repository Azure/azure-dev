package tools

import (
	"context"
	"fmt"
	"os"
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
	return "Get information about a file (size, modification time, permissions). Input: file path (e.g., 'data.txt' or './docs/readme.md')"
}

func (t FileInfoTool) Call(ctx context.Context, input string) (string, error) {
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolStart(ctx, fmt.Sprintf("file_info: %s", input))
	}

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

	var fileType string
	if info.IsDir() {
		fileType = "Directory"
	} else {
		fileType = "File"
	}

	output := fmt.Sprintf("%s: %s\nSize: %d bytes\nModified: %s\nPermissions: %s\n\n",
		fileType, input, info.Size(), info.ModTime().Format(time.RFC3339), info.Mode().String())

	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolEnd(ctx, output)
	}

	return output, nil
}
