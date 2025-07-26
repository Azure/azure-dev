package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DirectoryListTool implements the Tool interface for listing directory contents
type DirectoryListTool struct{}

func (t DirectoryListTool) Name() string {
	return "list_directory"
}

func (t DirectoryListTool) Description() string {
	return "List files and folders in a directory. Input: directory path (use '.' for current directory)"
}

func (t DirectoryListTool) Call(ctx context.Context, input string) (string, error) {
	path := strings.TrimSpace(input)
	if path == "" {
		path = "."
	}

	// Get absolute path for clarity
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path for %s: %w", path, err)
	}

	// Check if directory exists
	info, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("failed to access %s: %w", absPath, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", absPath)
	}

	// List directory contents
	files, err := os.ReadDir(absPath)
	if err != nil {
		return "", fmt.Errorf("failed to read directory %s: %w", absPath, err)
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Contents of %s:\n", absPath))
	result.WriteString(fmt.Sprintf("Total items: %d\n\n", len(files)))

	// Separate directories and files
	var dirs []string
	var regularFiles []string

	for _, file := range files {
		if file.IsDir() {
			dirs = append(dirs, file.Name()+"/")
		} else {
			info, err := file.Info()
			if err != nil {
				regularFiles = append(regularFiles, file.Name())
			} else {
				regularFiles = append(regularFiles, fmt.Sprintf("%s (%d bytes)", file.Name(), info.Size()))
			}
		}
	}

	// Display directories first
	if len(dirs) > 0 {
		result.WriteString("Directories:\n")
		for _, dir := range dirs {
			result.WriteString(fmt.Sprintf("  📁 %s\n", dir))
		}
		result.WriteString("\n")
	}

	// Then display files
	if len(regularFiles) > 0 {
		result.WriteString("Files:\n")
		for _, file := range regularFiles {
			result.WriteString(fmt.Sprintf("  📄 %s\n", file))
		}
	}

	if len(dirs) == 0 && len(regularFiles) == 0 {
		result.WriteString("Directory is empty.\n")
	}

	return result.String(), nil
}
