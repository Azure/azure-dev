package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/tmc/langchaingo/callbacks"
)

// FileSearchTool implements a tool for searching files using glob patterns
type FileSearchTool struct {
	CallbacksHandler callbacks.Handler
}

// FileSearchRequest represents the JSON payload for file search requests
type FileSearchRequest struct {
	Pattern    string `json:"pattern"`              // Glob pattern to match (required)
	MaxResults int    `json:"maxResults,omitempty"` // Optional: maximum number of results to return (default: 100)
}

func (t FileSearchTool) Name() string {
	return "file_search"
}

func (t FileSearchTool) Description() string {
	return `Search for files matching a glob pattern in the current working directory using the doublestar library for full glob support.

Input: JSON payload with the following structure:
{
  "pattern": "*.go",
  "maxResults": 50        // optional: max files to return (default: 100)
}

SUPPORTED GLOB PATTERNS (using github.com/bmatcuk/doublestar/v4):
- *.go - all Go files in current directory only
- **/*.js - all JavaScript files in current directory and all subdirectories
- test_*.py - Python files starting with "test_" in current directory only
- src/**/main.* - files named "main" with any extension in src directory tree
- *.{json,yaml,yml} - files with json, yaml, or yml extensions in current directory
- **/test/**/*.go - Go files in any test directory (recursive)
- [Tt]est*.py - files starting with "Test" or "test" in current directory
- {src,lib}/**/*.ts - TypeScript files in src or lib directories (recursive)
- !**/node_modules/** - exclude node_modules (negation patterns)

ADVANCED FEATURES:
- ** - matches zero or more directories (enables recursive search)
- ? - matches any single character
- * - matches any sequence of characters (except path separator)
- [abc] - matches any character in the set
- {pattern1,pattern2} - brace expansion
- !pattern - negation patterns (exclude matching files)

NOTE: Recursion is controlled by the glob pattern itself. Use ** to search subdirectories.

EXAMPLES:

Find all Go files:
{"pattern": "*.go"}

Find all test files recursively:
{"pattern": "**/test_*.py"}

Find config files with multiple extensions:
{"pattern": "*.{json,yaml,yml}", "maxResults": 20}

Find files excluding node_modules:
{"pattern": "**/*.js"}

Returns a sorted list of matching file paths relative to the current working directory.
The input must be formatted as a single line valid JSON string.`
}

func (t FileSearchTool) Call(ctx context.Context, input string) (string, error) {
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolStart(ctx, fmt.Sprintf("file_search: %s", input))
	}

	if input == "" {
		err := fmt.Errorf("input is required")
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, err)
		}
		return "", err
	}

	// Parse JSON input
	var req FileSearchRequest
	if err := json.Unmarshal([]byte(input), &req); err != nil {
		toolErr := fmt.Errorf("invalid JSON input: %w", err)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}

	// Validate required fields
	if req.Pattern == "" {
		err := fmt.Errorf("pattern is required")
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, err)
		}
		return "", err
	}

	// Set defaults
	if req.MaxResults == 0 {
		req.MaxResults = 100
	}

	// Get current working directory
	searchPath, err := os.Getwd()
	if err != nil {
		toolErr := fmt.Errorf("failed to get current working directory: %w", err)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}

	// Perform the search
	matches, err := t.searchFiles(searchPath, req.Pattern, req.MaxResults)
	if err != nil {
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, err)
		}
		return "", err
	}

	// Format output
	output := t.formatResults(searchPath, req.Pattern, matches, req.MaxResults)

	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolEnd(ctx, output)
	}

	return output, nil
}

// searchFiles performs the actual file search using doublestar for comprehensive glob matching
func (t FileSearchTool) searchFiles(searchPath, pattern string, maxResults int) ([]string, error) {
	var matches []string
	searchPath = filepath.Clean(searchPath)

	// Use doublestar.Glob which handles all advanced patterns including recursion via **
	globPattern := filepath.Join(searchPath, pattern)
	// Convert to forward slashes for cross-platform compatibility
	globPattern = filepath.ToSlash(globPattern)

	globMatches, err := doublestar.FilepathGlob(globPattern)
	if err != nil {
		return nil, fmt.Errorf("error in glob pattern matching: %w", err)
	}

	// Convert to relative paths and limit results
	for _, match := range globMatches {
		if len(matches) >= maxResults {
			break
		}

		// Check if it's a file (not directory)
		info, err := os.Stat(match)
		if err != nil || info.IsDir() {
			continue
		}

		relPath, err := filepath.Rel(searchPath, match)
		if err != nil {
			continue // Skip files we can't get relative path for
		}

		// Convert to forward slashes for consistent output
		relPath = filepath.ToSlash(relPath)
		matches = append(matches, relPath)
	}

	// Sort the results for consistent output
	sort.Strings(matches)

	return matches, nil
}

// formatResults formats the search results into a readable output
func (t FileSearchTool) formatResults(searchPath, pattern string, matches []string, maxResults int) string {
	var output strings.Builder

	output.WriteString("File search results:\n")
	output.WriteString(fmt.Sprintf("Current directory: %s\n", searchPath))
	output.WriteString(fmt.Sprintf("Pattern: %s\n", pattern))
	output.WriteString(fmt.Sprintf("Found %d file(s)", len(matches)))

	if len(matches) >= maxResults {
		output.WriteString(fmt.Sprintf(" (limited to %d results)", maxResults))
	}
	output.WriteString("\n\n")

	if len(matches) == 0 {
		output.WriteString("No files found matching the pattern.\n")
		return output.String()
	}

	output.WriteString("Matching files:\n")
	for i, match := range matches {
		output.WriteString(fmt.Sprintf("%3d. %s\n", i+1, match))
	}

	if len(matches) >= maxResults {
		output.WriteString(fmt.Sprintf("\n⚠️  Results limited to %d files. Use maxResults parameter to adjust limit.\n", maxResults))
	}

	return output.String()
}
