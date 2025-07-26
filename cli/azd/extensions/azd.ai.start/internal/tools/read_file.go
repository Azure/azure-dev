package tools

import (
	"context"
	"fmt"
	"os"
)

// ReadFileTool implements the Tool interface for reading file contents
type ReadFileTool struct{}

func (t ReadFileTool) Name() string {
	return "read_file"
}

func (t ReadFileTool) Description() string {
	return "Read the contents of a file. Input: file path (e.g., 'README.md' or './docs/setup.md')"
}

func (t ReadFileTool) Call(ctx context.Context, input string) (string, error) {
	if input == "" {
		return "", fmt.Errorf("file path is required")
	}

	content, err := os.ReadFile(input)
	if err != nil {
		return "", fmt.Errorf("failed to read file %s: %w", input, err)
	}

	// Limit file size to avoid overwhelming context
	if len(content) > 5000 {
		return fmt.Sprintf("File: %s (first 5000 chars)\n%s...\n[File truncated - total size: %d bytes]",
			input, string(content[:5000]), len(content)), nil
	}

	return fmt.Sprintf("File: %s\n%s", input, string(content)), nil
}
