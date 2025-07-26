package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// FileInfoTool implements the Tool interface for getting detailed file information
type FileInfoTool struct{}

func (t FileInfoTool) Name() string {
	return "file_info"
}

func (t FileInfoTool) Description() string {
	return "Get detailed information about a file or directory. Input: file or directory path (e.g., 'README.md' or './docs')"
}

func (t FileInfoTool) Call(ctx context.Context, input string) (string, error) {
	if input == "" {
		return "", fmt.Errorf("file or directory path is required")
	}

	info, err := os.Stat(input)
	if err != nil {
		return "", fmt.Errorf("failed to get info for %s: %w", input, err)
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Information for: %s\n", input))
	result.WriteString("═══════════════════════════════════\n")

	// Type
	if info.IsDir() {
		result.WriteString("Type: Directory\n")

		// Count contents if it's a directory
		if files, err := os.ReadDir(input); err == nil {
			result.WriteString(fmt.Sprintf("Contents: %d items\n", len(files)))
		}
	} else {
		result.WriteString("Type: File\n")
		result.WriteString(fmt.Sprintf("Size: %d bytes\n", info.Size()))
	}

	// Permissions
	result.WriteString(fmt.Sprintf("Permissions: %s\n", info.Mode().String()))

	// Timestamps
	result.WriteString(fmt.Sprintf("Modified: %s\n", info.ModTime().Format(time.RFC3339)))

	// Additional file details
	if !info.IsDir() {
		if info.Size() == 0 {
			result.WriteString("Note: File is empty\n")
		} else if info.Size() > 1024*1024 {
			result.WriteString(fmt.Sprintf("Size (human): %.2f MB\n", float64(info.Size())/(1024*1024)))
		} else if info.Size() > 1024 {
			result.WriteString(fmt.Sprintf("Size (human): %.2f KB\n", float64(info.Size())/1024))
		}
	}

	return result.String(), nil
}
