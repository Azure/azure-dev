package tools

import (
	"context"
	"fmt"
	"os"
)

// CreateDirectoryTool implements the Tool interface for creating directories
type CreateDirectoryTool struct{}

func (t CreateDirectoryTool) Name() string {
	return "create_directory"
}

func (t CreateDirectoryTool) Description() string {
	return "Create a directory (and any necessary parent directories). Input: directory path (e.g., 'docs' or './src/components')"
}

func (t CreateDirectoryTool) Call(ctx context.Context, input string) (string, error) {
	if input == "" {
		return "", fmt.Errorf("directory path is required")
	}

	err := os.MkdirAll(input, 0755)
	if err != nil {
		return "", fmt.Errorf("failed to create directory %s: %w", input, err)
	}

	// Check if directory already existed or was newly created
	info, err := os.Stat(input)
	if err != nil {
		return "", fmt.Errorf("failed to verify directory creation: %w", err)
	}

	if !info.IsDir() {
		return "", fmt.Errorf("%s exists but is not a directory", input)
	}

	return fmt.Sprintf("Successfully created directory: %s", input), nil
}
