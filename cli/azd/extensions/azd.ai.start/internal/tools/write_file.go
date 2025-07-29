package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tmc/langchaingo/callbacks"
)

// WriteFileTool implements the Tool interface for writing file contents
type WriteFileTool struct {
	CallbacksHandler callbacks.Handler
}

// WriteFileRequest represents the JSON input for the write_file tool
type WriteFileRequest struct {
	Filename string `json:"filename"`
	Content  string `json:"content"`
}

func (t WriteFileTool) Name() string {
	return "write_file"
}

func (t WriteFileTool) Description() string {
	return "Writes content to a file.  Format input as a single line JSON payload with a 'filename' and 'content' parameters."
}

func (t WriteFileTool) Call(ctx context.Context, input string) (string, error) {
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolStart(ctx, fmt.Sprintf("write_file: %s", input))
	}

	if input == "" {
		err := fmt.Errorf("input is required as JSON object with filename and content fields")
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, err)
		}
		return "", err
	}

	// Parse JSON input
	var req WriteFileRequest
	if err := json.Unmarshal([]byte(input), &req); err != nil {
		toolErr := fmt.Errorf("invalid JSON input: %w", err)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}

	if req.Filename == "" {
		err := fmt.Errorf("filename cannot be empty")
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, err)
		}
		return "", err
	}

	filePath := strings.TrimSpace(req.Filename)
	content := req.Content

	// Convert literal \n sequences to actual newlines (for agents that escape newlines)
	content = strings.ReplaceAll(content, "\\n", "\n")
	content = strings.ReplaceAll(content, "\\t", "\t")

	// Ensure the directory exists
	dir := filepath.Dir(filePath)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			toolErr := fmt.Errorf("failed to create directory %s: %w", dir, err)
			if t.CallbacksHandler != nil {
				t.CallbacksHandler.HandleToolError(ctx, toolErr)
			}
			return "", toolErr
		}
	}

	// Write the file
	err := os.WriteFile(filePath, []byte(content), 0644)
	if err != nil {
		toolErr := fmt.Errorf("failed to write file %s: %w", filePath, err)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}

	// Verify the file was written correctly
	writtenContent, err := os.ReadFile(filePath)
	if err != nil {
		toolErr := fmt.Errorf("failed to verify written file %s: %w", filePath, err)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}

	lineCount := strings.Count(string(writtenContent), "\n") + 1
	if content != "" && !strings.HasSuffix(content, "\n") {
		lineCount = strings.Count(content, "\n") + 1
	}

	output := fmt.Sprintf("Successfully wrote %d bytes (%d lines) to %s. Content preview:\n%s",
		len(content), lineCount, filePath, getContentPreview(content))

	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolEnd(ctx, output)
	}

	return output, nil
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
