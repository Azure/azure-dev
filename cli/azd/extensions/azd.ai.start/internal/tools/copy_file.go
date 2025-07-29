package tools

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

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
	return "Copy a file to a new location. Input format: 'source|destination' (e.g., 'file.txt|backup.txt' or './docs/readme.md|./backup/readme.md')"
}

func (t CopyFileTool) Call(ctx context.Context, input string) (string, error) {
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolStart(ctx, fmt.Sprintf("copy_file: %s", input))
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

	// Check if source file exists
	sourceInfo, err := os.Stat(source)
	if err != nil {
		toolErr := fmt.Errorf("source file %s does not exist: %w", source, err)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}

	if sourceInfo.IsDir() {
		err := fmt.Errorf("source %s is a directory. Use copy_directory for directories", source)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, err)
		}
		return "", err
	}

	// Open source file
	sourceFile, err := os.Open(source)
	if err != nil {
		toolErr := fmt.Errorf("failed to open source file %s: %w", source, err)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}
	defer sourceFile.Close()

	// Create destination file
	destFile, err := os.Create(destination)
	if err != nil {
		toolErr := fmt.Errorf("failed to create destination file %s: %w", destination, err)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}
	defer destFile.Close()

	// Copy contents
	bytesWritten, err := io.Copy(destFile, sourceFile)
	if err != nil {
		toolErr := fmt.Errorf("failed to copy file: %w", err)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}

	output := fmt.Sprintf("Copied %s to %s (%d bytes)\n", source, destination, bytesWritten)
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolEnd(ctx, output)
	}

	return output, nil
}
