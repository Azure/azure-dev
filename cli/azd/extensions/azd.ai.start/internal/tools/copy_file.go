package tools

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

// CopyFileTool implements the Tool interface for copying files
type CopyFileTool struct{}

func (t CopyFileTool) Name() string {
	return "copy_file"
}

func (t CopyFileTool) Description() string {
	return "Copy a file to a new location. Input format: 'source|destination' (e.g., 'file.txt|backup.txt' or './docs/readme.md|./backup/readme.md')"
}

func (t CopyFileTool) Call(ctx context.Context, input string) (string, error) {
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

	// Check if source file exists
	sourceInfo, err := os.Stat(source)
	if err != nil {
		return "", fmt.Errorf("source file %s does not exist: %w", source, err)
	}

	if sourceInfo.IsDir() {
		return "", fmt.Errorf("source %s is a directory. Use copy_directory for directories", source)
	}

	// Open source file
	sourceFile, err := os.Open(source)
	if err != nil {
		return "", fmt.Errorf("failed to open source file %s: %w", source, err)
	}
	defer sourceFile.Close()

	// Create destination file
	destFile, err := os.Create(destination)
	if err != nil {
		return "", fmt.Errorf("failed to create destination file %s: %w", destination, err)
	}
	defer destFile.Close()

	// Copy contents
	bytesWritten, err := io.Copy(destFile, sourceFile)
	if err != nil {
		return "", fmt.Errorf("failed to copy file: %w", err)
	}

	return fmt.Sprintf("Successfully copied %s to %s (%d bytes)", source, destination, bytesWritten), nil
}
