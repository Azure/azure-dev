package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/tmc/langchaingo/callbacks"
)

// MoveFileTool implements the Tool interface for moving/renaming files
type MoveFileTool struct {
	CallbacksHandler callbacks.Handler
}

func (t MoveFileTool) Name() string {
	return "move_file"
}

func (t MoveFileTool) Description() string {
	return "Move or rename a file. Input format: 'source|destination' (e.g., 'old.txt|new.txt' or './file.txt|./folder/file.txt')"
}

func (t MoveFileTool) Call(ctx context.Context, input string) (string, error) {
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolStart(ctx, fmt.Sprintf("move_file: %s", input))
	}

	if input == "" {
		err := fmt.Errorf("input is required in format 'source|destination'")
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, err)
		}
		return "", err
	}

	// Split on first occurrence of '|' to separate source from destination
	parts := strings.SplitN(input, "|", 2)
	if len(parts) != 2 {
		err := fmt.Errorf("invalid input format. Use 'source|destination'")
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, err)
		}
		return "", err
	}

	source := strings.TrimSpace(parts[0])
	destination := strings.TrimSpace(parts[1])

	if source == "" || destination == "" {
		err := fmt.Errorf("both source and destination paths are required")
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, err)
		}
		return "", err
	}

	// Check if source exists
	sourceInfo, err := os.Stat(source)
	if err != nil {
		toolErr := fmt.Errorf("source %s does not exist: %w", source, err)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}

	// Check if destination already exists
	if _, err := os.Stat(destination); err == nil {
		err := fmt.Errorf("destination %s already exists", destination)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, err)
		}
		return "", err
	}

	// Move/rename the file
	err = os.Rename(source, destination)
	if err != nil {
		toolErr := fmt.Errorf("failed to move %s to %s: %w", source, destination, err)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}

	fileType := "file"
	if sourceInfo.IsDir() {
		fileType = "directory"
	}

	output := fmt.Sprintf("Successfully moved %s from %s to %s (%d bytes)", fileType, source, destination, sourceInfo.Size())
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolEnd(ctx, output)
	}

	return output, nil
}
