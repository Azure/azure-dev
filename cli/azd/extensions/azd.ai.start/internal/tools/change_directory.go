package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// ChangeDirectoryTool implements the Tool interface for changing the current working directory
type ChangeDirectoryTool struct{}

func (t ChangeDirectoryTool) Name() string {
	return "change_directory"
}

func (t ChangeDirectoryTool) Description() string {
	return "Change the current working directory. Input: directory path (e.g., '../parent' or './subfolder' or absolute path)"
}

func (t ChangeDirectoryTool) Call(ctx context.Context, input string) (string, error) {
	if input == "" {
		return "", fmt.Errorf("directory path is required")
	}

	// Get current directory for reference
	currentDir, _ := os.Getwd()

	// Convert to absolute path
	absPath, err := filepath.Abs(input)
	if err != nil {
		return "", fmt.Errorf("failed to resolve path %s: %w", input, err)
	}

	// Check if directory exists
	info, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("directory %s does not exist: %w", absPath, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", absPath)
	}

	// Change directory
	err = os.Chdir(absPath)
	if err != nil {
		return "", fmt.Errorf("failed to change directory to %s: %w", absPath, err)
	}

	return fmt.Sprintf("Changed directory from %s to %s", currentDir, absPath), nil
}
