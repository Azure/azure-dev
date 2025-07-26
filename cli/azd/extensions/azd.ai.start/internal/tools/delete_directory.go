package tools

import (
	"context"
	"fmt"
	"os"
)

// DeleteDirectoryTool implements the Tool interface for deleting directories
type DeleteDirectoryTool struct{}

func (t DeleteDirectoryTool) Name() string {
	return "delete_directory"
}

func (t DeleteDirectoryTool) Description() string {
	return "Delete a directory and all its contents. Input: directory path (e.g., 'temp-folder' or './old-docs')"
}

func (t DeleteDirectoryTool) Call(ctx context.Context, input string) (string, error) {
	if input == "" {
		return "", fmt.Errorf("directory path is required")
	}

	// Check if directory exists
	info, err := os.Stat(input)
	if err != nil {
		return "", fmt.Errorf("directory %s does not exist: %w", input, err)
	}

	// Make sure it's a directory, not a file
	if !info.IsDir() {
		return "", fmt.Errorf("%s is a file, not a directory. Use delete_file to remove files", input)
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
		return "", fmt.Errorf("failed to delete directory %s: %w", input, err)
	}

	if fileCount > 0 {
		return fmt.Sprintf("Successfully deleted directory: %s (contained %d items)", input, fileCount), nil
	}
	return fmt.Sprintf("Successfully deleted empty directory: %s", input), nil
}
