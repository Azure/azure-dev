package tools

import (
	"context"
	"fmt"
	"os"
)

// DeleteFileTool implements the Tool interface for deleting files
type DeleteFileTool struct{}

func (t DeleteFileTool) Name() string {
	return "delete_file"
}

func (t DeleteFileTool) Description() string {
	return "Delete a file. Input: file path (e.g., 'temp.txt' or './docs/old-file.md')"
}

func (t DeleteFileTool) Call(ctx context.Context, input string) (string, error) {
	if input == "" {
		return "", fmt.Errorf("file path is required")
	}

	// Check if file exists and get info
	info, err := os.Stat(input)
	if err != nil {
		return "", fmt.Errorf("file %s does not exist: %w", input, err)
	}

	// Make sure it's a file, not a directory
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory, not a file. Use delete_directory to remove directories", input)
	}

	// Delete the file
	err = os.Remove(input)
	if err != nil {
		return "", fmt.Errorf("failed to delete file %s: %w", input, err)
	}

	return fmt.Sprintf("Successfully deleted file: %s (%d bytes)", input, info.Size()), nil
}
