package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WriteFileTool implements the Tool interface for writing file contents
type WriteFileTool struct{}

func (t WriteFileTool) Name() string {
	return "write_file"
}

func (t WriteFileTool) Description() string {
	return `Write content to a file. Input format: 'filepath|content'

For multi-line content, use literal \n for newlines:
- Single line: 'test.txt|Hello World'  
- Multi-line: 'script.bicep|param name string\nparam location string\nresource myResource...'

Example Bicep file:
'main.bicep|param name string\nparam location string\n\nresource appService ''Microsoft.Web/sites@2022-03-01'' = {\n  name: name\n  location: location\n  kind: ''app''\n  properties: {\n    serverFarmId: serverFarmId\n  }\n}\n\noutput appServiceId string = appService.id'

The tool will convert \n to actual newlines automatically.`
}

func (t WriteFileTool) Call(ctx context.Context, input string) (string, error) {
	if input == "" {
		return "", fmt.Errorf("input is required in format 'filepath|content'")
	}

	// Split on first occurrence of '|' to separate path from content
	parts := strings.SplitN(input, "|", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid input format. Use 'filepath|content'")
	}

	filePath := strings.TrimSpace(parts[0])
	content := parts[1]

	// Convert literal \n sequences to actual newlines (for agents that escape newlines)
	content = strings.ReplaceAll(content, "\\n", "\n")
	content = strings.ReplaceAll(content, "\\t", "\t")

	// Clean up any trailing quotes that might have been added during formatting
	content = strings.TrimSuffix(content, "'")
	content = strings.TrimSuffix(content, "\")")

	// Clean up any quotes around the filepath (from agent formatting)
	filePath = strings.Trim(filePath, "\"'")

	if filePath == "" {
		return "", fmt.Errorf("filepath cannot be empty")
	}

	// Ensure the directory exists
	dir := filepath.Dir(filePath)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// Write the file
	err := os.WriteFile(filePath, []byte(content), 0644)
	if err != nil {
		return "", fmt.Errorf("failed to write file %s: %w", filePath, err)
	}

	// Verify the file was written correctly
	writtenContent, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to verify written file %s: %w", filePath, err)
	}

	lineCount := strings.Count(string(writtenContent), "\n") + 1
	if content != "" && !strings.HasSuffix(content, "\n") {
		lineCount = strings.Count(content, "\n") + 1
	}

	return fmt.Sprintf("Successfully wrote %d bytes (%d lines) to %s. Content preview:\n%s",
		len(content), lineCount, filePath, getContentPreview(content)), nil
}

// getContentPreview returns a preview of the content for verification
func getContentPreview(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) <= 5 {
		return content
	}

	preview := strings.Join(lines[:3], "\n")
	preview += fmt.Sprintf("\n... (%d more lines) ...\n", len(lines)-5)
	preview += strings.Join(lines[len(lines)-2:], "\n")

	return preview
}
