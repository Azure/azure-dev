// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package io

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/mark3labs/mcp-go/mcp"
)

// FileSearchTool implements a tool for searching files using glob patterns
type FileSearchTool struct {
	common.BuiltInTool
}

// FileSearchRequest represents the JSON payload for file search requests
type FileSearchRequest struct {
	Pattern    string `json:"pattern"`              // Glob pattern to match (required)
	MaxResults int    `json:"maxResults,omitempty"` // Optional: maximum number of results to return (default: 100)
}

func (t FileSearchTool) Name() string {
	return "file_search"
}

func (t FileSearchTool) Annotations() mcp.ToolAnnotation {
	return mcp.ToolAnnotation{
		Title:           "Search Files by Pattern",
		ReadOnlyHint:    common.ToPtr(true),
		DestructiveHint: common.ToPtr(false),
		IdempotentHint:  common.ToPtr(true),
		OpenWorldHint:   common.ToPtr(false),
	}
}

func (t FileSearchTool) Description() string {
	return `Searches for files matching a glob pattern in the current working directory 
using the doublestar library for full glob support.

Input: JSON payload with the following structure:
{
  "pattern": "*.go",
  "maxResults": 50        // optional: max files to return (default: 100)
}

Returns JSON with search results and metadata.

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

// createErrorResponse creates a JSON error response
func (t FileSearchTool) createErrorResponse(err error, message string) (string, error) {
	if message == "" {
		message = err.Error()
	}

	errorResp := common.ErrorResponse{
		Error:   true,
		Message: message,
	}

	jsonData, jsonErr := json.MarshalIndent(errorResp, "", "  ")
	if jsonErr != nil {
		// Fallback to simple error message if JSON marshalling fails
		fallbackMsg := fmt.Sprintf(`{"error": true, "message": "JSON marshalling failed: %s"}`, jsonErr.Error())
		return fallbackMsg, nil
	}

	return string(jsonData), nil
}

func (t FileSearchTool) Call(ctx context.Context, input string) (string, error) {
	if input == "" {
		return t.createErrorResponse(
			fmt.Errorf("input is required"),
			"Input is required. Expected JSON format: {\"pattern\": \"*.go\"}",
		)
	}

	// Parse JSON input
	var req FileSearchRequest
	if err := json.Unmarshal([]byte(input), &req); err != nil {
		return t.createErrorResponse(
			err,
			fmt.Sprintf("Invalid JSON input: %s. Expected format: {\"pattern\": \"*.go\", \"maxResults\": 50}", err.Error()),
		)
	}

	// Validate required fields
	if req.Pattern == "" {
		return t.createErrorResponse(fmt.Errorf("pattern is required"), "Pattern is required in the JSON input")
	}

	// Set default max results
	maxResults := req.MaxResults
	if maxResults <= 0 {
		maxResults = 100
	}

	// Use doublestar to find matching files
	matches, err := doublestar.FilepathGlob(req.Pattern)
	if err != nil {
		return t.createErrorResponse(err, fmt.Sprintf("Invalid glob pattern '%s': %s", req.Pattern, err.Error()))
	}

	// Sort results for consistent output
	sort.Strings(matches)

	// Limit results if needed
	if len(matches) > maxResults {
		matches = matches[:maxResults]
	}

	// Create response structure
	type FileSearchResponse struct {
		Success    bool     `json:"success"`
		Pattern    string   `json:"pattern"`
		TotalFound int      `json:"totalFound"`
		Returned   int      `json:"returned"`
		MaxResults int      `json:"maxResults"`
		Files      []string `json:"files"`
		Message    string   `json:"message"`
	}

	totalFound := len(matches)
	returned := len(matches)

	var message string
	if totalFound == 0 {
		message = fmt.Sprintf("No files found matching pattern '%s'", req.Pattern)
	} else if totalFound == returned {
		message = fmt.Sprintf("Found %d files matching pattern '%s'", totalFound, req.Pattern)
	} else {
		message = fmt.Sprintf("Found %d files matching pattern '%s', returning first %d", totalFound, req.Pattern, returned)
	}

	response := FileSearchResponse{
		Success:    true,
		Pattern:    req.Pattern,
		TotalFound: totalFound,
		Returned:   returned,
		MaxResults: maxResults,
		Files:      matches,
		Message:    message,
	}

	// Convert to JSON
	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return t.createErrorResponse(err, fmt.Sprintf("Failed to marshal JSON response: %s", err.Error()))
	}

	return string(jsonData), nil
}
