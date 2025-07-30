package io

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tmc/langchaingo/callbacks"
)

// ReadFileTool implements the Tool interface for reading file contents
type ReadFileTool struct {
	CallbacksHandler callbacks.Handler
}

// ReadFileRequest represents the JSON payload for file read requests
type ReadFileRequest struct {
	FilePath  string `json:"filePath"`
	StartLine int    `json:"startLine,omitempty"` // Optional: 1-based line number to start reading from
	EndLine   int    `json:"endLine,omitempty"`   // Optional: 1-based line number to end reading at
}

// ReadFileResponse represents the JSON output for the read_file tool
type ReadFileResponse struct {
	Success     bool         `json:"success"`
	FilePath    string       `json:"filePath"`
	Content     string       `json:"content"`
	IsTruncated bool         `json:"isTruncated"`
	IsPartial   bool         `json:"isPartial"`
	LineRange   *LineRange   `json:"lineRange,omitempty"`
	FileInfo    ReadFileInfo `json:"fileInfo"`
	Message     string       `json:"message,omitempty"`
}

// LineRange represents the range of lines read
type LineRange struct {
	StartLine  int `json:"startLine"`
	EndLine    int `json:"endLine"`
	TotalLines int `json:"totalLines"`
	LinesRead  int `json:"linesRead"`
}

// ReadFileInfo represents file metadata for read operations
type ReadFileInfo struct {
	Size         int64     `json:"size"`
	ModifiedTime time.Time `json:"modifiedTime"`
	Permissions  string    `json:"permissions"`
}

func (t ReadFileTool) Name() string {
	return "read_file"
}

func (t ReadFileTool) Description() string {
	return `Read file contents with intelligent handling for different file sizes and partial reads. Returns JSON response with file content and metadata.

Input: JSON payload with the following structure:
{
  "filePath": "path/to/file.txt",
  "startLine": 10,    // optional: 1-based line number to start reading from
  "endLine": 50       // optional: 1-based line number to end reading at
}

Examples:
1. Read entire file:
   {"filePath": "README.md"}

2. Read specific line range:
   {"filePath": "src/main.go", "startLine": 1, "endLine": 100}

3. Read from line to end:
   {"filePath": "config.go", "startLine": 25}

4. Read from start to line:
   {"filePath": "app.py", "endLine": 30}

5. Read single line:
   {"filePath": "package.json", "startLine": 42, "endLine": 42}

Files larger than 10KB are automatically truncated. Files over 1MB show size info only unless specific line range is requested.
The input must be formatted as a single line valid JSON string.`
}

func (t ReadFileTool) Call(ctx context.Context, input string) (string, error) {
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolStart(ctx, fmt.Sprintf("read_file: %s", input))
	}

	if input == "" {
		output := "❌ No input provided\n\n"
		output += "📝 Expected JSON format:\n"
		output += `{"filePath": "path/to/file.txt"}`

		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, fmt.Errorf("empty input"))
			t.CallbacksHandler.HandleToolEnd(ctx, output)
		}
		return output, nil
	}

	// Parse JSON input
	var req ReadFileRequest
	if err := json.Unmarshal([]byte(input), &req); err != nil {
		output := fmt.Sprintf("❌ Invalid JSON input: %s\n\n", err.Error())
		output += "📝 Expected format:\n"
		output += `{"filePath": "path/to/file.txt", "startLine": 1, "endLine": 50}`
		output += "\n\n💡 Tips:\n"
		output += "- Use double quotes for strings\n"
		output += "- Remove any trailing commas\n"
		output += "- Escape backslashes: use \\\\ instead of \\"

		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, err)
			t.CallbacksHandler.HandleToolEnd(ctx, output)
		}
		return output, nil
	}

	// Validate required fields
	if req.FilePath == "" {
		output := "❌ Missing required field: filePath cannot be empty\n\n"
		output += "📝 Example: " + `{"filePath": "README.md"}`

		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, fmt.Errorf("missing filePath"))
			t.CallbacksHandler.HandleToolEnd(ctx, output)
		}
		return output, nil
	}

	// Get file info first to check size
	fileInfo, err := os.Stat(req.FilePath)
	if err != nil {
		output := fmt.Sprintf("❌ Cannot access file: %s\n\n", req.FilePath)
		if os.IsNotExist(err) {
			output += "📁 File does not exist. Please check:\n"
			output += "- File path spelling and case sensitivity\n"
			output += "- File location relative to current directory\n"
			output += "- File permissions\n"
		} else {
			output += fmt.Sprintf("Error details: %s\n", err.Error())
		}

		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, err)
			t.CallbacksHandler.HandleToolEnd(ctx, output)
		}
		return output, nil
	}

	fileSize := fileInfo.Size()

	// Handle very large files differently (unless specific line range requested)
	if fileSize > 1024*1024 && req.StartLine == 0 && req.EndLine == 0 { // 1MB+
		response := ReadFileResponse{
			Success:     false,
			FilePath:    req.FilePath,
			Content:     "",
			IsTruncated: false,
			IsPartial:   false,
			FileInfo: ReadFileInfo{
				Size:         fileSize,
				ModifiedTime: fileInfo.ModTime(),
				Permissions:  fileInfo.Mode().String(),
			},
			Message: fmt.Sprintf("File is very large (%.2f MB). Use startLine and endLine parameters for specific sections.", float64(fileSize)/(1024*1024)),
		}

		jsonData, err := json.MarshalIndent(response, "", "  ")
		if err != nil {
			toolErr := fmt.Errorf("failed to marshal JSON response: %w", err)
			if t.CallbacksHandler != nil {
				t.CallbacksHandler.HandleToolError(ctx, toolErr)
			}
			return "", toolErr
		}

		output := string(jsonData)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolEnd(ctx, output)
		}
		return output, nil
	}

	content, err := os.ReadFile(req.FilePath)
	if err != nil {
		output := fmt.Sprintf("❌ Cannot read file: %s\n", req.FilePath)
		output += fmt.Sprintf("Error: %s\n\n", err.Error())
		output += "💡 This might be due to:\n"
		output += "- Insufficient permissions\n"
		output += "- File is locked by another process\n"
		output += "- File is binary or corrupted\n"

		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, err)
			t.CallbacksHandler.HandleToolEnd(ctx, output)
		}
		return output, nil
	}

	lines := strings.Split(string(content), "\n")
	totalLines := len(lines)

	// Handle partial reads based on line range
	if req.StartLine > 0 || req.EndLine > 0 {
		return t.handlePartialRead(ctx, req.FilePath, lines, req.StartLine, req.EndLine, totalLines, fileInfo)
	}

	var finalContent string
	var isTruncated bool
	var message string

	// Improved truncation with better limits for full file reads
	if len(content) > 10000 { // 10KB limit
		// Show first 50 lines and last 10 lines
		preview := strings.Join(lines[:50], "\n")
		if totalLines > 60 {
			preview += fmt.Sprintf("\n\n... [%d lines omitted] ...\n\n", totalLines-60)
			preview += strings.Join(lines[totalLines-10:], "\n")
		}
		finalContent = preview
		isTruncated = true
		message = "Large file truncated - showing first 50 and last 10 lines"
	} else {
		finalContent = string(content)
		isTruncated = false
		message = "File read successfully"
	}

	response := ReadFileResponse{
		Success:     true,
		FilePath:    req.FilePath,
		Content:     finalContent,
		IsTruncated: isTruncated,
		IsPartial:   false,
		FileInfo: ReadFileInfo{
			Size:         fileSize,
			ModifiedTime: fileInfo.ModTime(),
			Permissions:  fileInfo.Mode().String(),
		},
		Message: message,
	}

	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		toolErr := fmt.Errorf("failed to marshal JSON response: %w", err)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}

	output := string(jsonData)
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolEnd(ctx, output)
	}

	return output, nil
}

// handlePartialRead handles reading specific line ranges from a file
func (t ReadFileTool) handlePartialRead(ctx context.Context, filePath string, lines []string, startLine, endLine, totalLines int, fileInfo os.FileInfo) (string, error) {
	// Validate and adjust line numbers (1-based to 0-based)
	if startLine == 0 {
		startLine = 1 // Default to start of file
	}
	if endLine == 0 {
		endLine = totalLines // Default to end of file
	}

	// Validate line numbers
	if startLine < 1 {
		startLine = 1
	}
	if endLine > totalLines {
		endLine = totalLines
	}
	if startLine > endLine {
		response := ReadFileResponse{
			Success:     false,
			FilePath:    filePath,
			Content:     "",
			IsTruncated: false,
			IsPartial:   false,
			FileInfo: ReadFileInfo{
				Size:         fileInfo.Size(),
				ModifiedTime: fileInfo.ModTime(),
				Permissions:  fileInfo.Mode().String(),
			},
			Message: fmt.Sprintf("Invalid line range: start line (%d) cannot be greater than end line (%d)", startLine, endLine),
		}

		jsonData, err := json.MarshalIndent(response, "", "  ")
		if err != nil {
			toolErr := fmt.Errorf("failed to marshal JSON response: %w", err)
			if t.CallbacksHandler != nil {
				t.CallbacksHandler.HandleToolError(ctx, toolErr)
			}
			return "", toolErr
		}

		output := string(jsonData)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, fmt.Errorf("invalid line range: start %d > end %d", startLine, endLine))
			t.CallbacksHandler.HandleToolEnd(ctx, output)
		}
		return output, nil
	}

	// Convert to 0-based indexing
	startIdx := startLine - 1
	endIdx := endLine

	// Extract the requested lines
	selectedLines := lines[startIdx:endIdx]
	content := strings.Join(selectedLines, "\n")

	linesRead := endLine - startLine + 1

	response := ReadFileResponse{
		Success:     true,
		FilePath:    filePath,
		Content:     content,
		IsTruncated: false,
		IsPartial:   true,
		LineRange: &LineRange{
			StartLine:  startLine,
			EndLine:    endLine,
			TotalLines: totalLines,
			LinesRead:  linesRead,
		},
		FileInfo: ReadFileInfo{
			Size:         fileInfo.Size(),
			ModifiedTime: fileInfo.ModTime(),
			Permissions:  fileInfo.Mode().String(),
		},
		Message: fmt.Sprintf("Successfully read %d lines (%d-%d) from file", linesRead, startLine, endLine),
	}

	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		toolErr := fmt.Errorf("failed to marshal JSON response: %w", err)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}

	output := string(jsonData)
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolEnd(ctx, output)
	}

	return output, nil
}
