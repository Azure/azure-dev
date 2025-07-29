package tools

import (
	"context"
	"fmt"
	"os"

	"github.com/tmc/langchaingo/callbacks"
)

// ReadFileTool implements the Tool interface for reading file contents
type ReadFileTool struct {
	CallbacksHandler callbacks.Handler
}

func (t ReadFileTool) Name() string {
	return "read_file"
}

func (t ReadFileTool) Description() string {
	return "Read the contents of a file. Input: file path (e.g., 'README.md' or './docs/setup.md')"
}

func (t ReadFileTool) Call(ctx context.Context, input string) (string, error) {
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolStart(ctx, fmt.Sprintf("read_file: %s", input))
	}

	if input == "" {
		err := fmt.Errorf("file path is required")
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, err)
		}
		return "", err
	}

	content, err := os.ReadFile(input)
	if err != nil {
		toolErr := fmt.Errorf("failed to read file %s: %w", input, err)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}

	var output string
	// Limit file size to avoid overwhelming context
	if len(content) > 5000 {
		output = fmt.Sprintf("File: %s (first 5000 chars)\n%s...\n[File truncated - total size: %d bytes]",
			input, string(content[:5000]), len(content))
	} else {
		output = fmt.Sprintf("File: %s\n%s", input, string(content))
	}

	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolEnd(ctx, output)
	}

	return output, nil
}
