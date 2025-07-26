package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// MoveFileTool implements the Tool interface for moving/renaming files
type MoveFileTool struct{}

func (t MoveFileTool) Name() string {
	return "move_file"
}

func (t MoveFileTool) Description() string {
	return "Move or rename a file. Input format: 'source|destination' (e.g., 'old.txt|new.txt' or './file.txt|./folder/file.txt')"
}

func (t MoveFileTool) Call(ctx context.Context, input string) (string, error) {
	if input == "" {
		return "", fmt.Errorf("input is required in format 'source|destination'")
	}

	// Split on first occurrence of '|' to separate source from destination
	parts := strings.SplitN(input, "|", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid input format. Use 'source|destination'")
	}

	source := strings.TrimSpace(parts[0])
	destination := strings.TrimSpace(parts[1])

	if source == "" || destination == "" {
		return "", fmt.Errorf("both source and destination paths are required")
	}

	// Check if source exists
	sourceInfo, err := os.Stat(source)
	if err != nil {
		return "", fmt.Errorf("source %s does not exist: %w", source, err)
	}

	// Check if destination already exists
	if _, err := os.Stat(destination); err == nil {
		return "", fmt.Errorf("destination %s already exists", destination)
	}

	// Move/rename the file
	err = os.Rename(source, destination)
	if err != nil {
		return "", fmt.Errorf("failed to move %s to %s: %w", source, destination, err)
	}

	fileType := "file"
	if sourceInfo.IsDir() {
		fileType = "directory"
	}

	return fmt.Sprintf("Successfully moved %s from %s to %s (%d bytes)", fileType, source, destination, sourceInfo.Size()), nil
}
