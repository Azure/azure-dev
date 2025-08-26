// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package http

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
	"github.com/mark3labs/mcp-go/mcp"
)

// HTTPFetcherTool implements the Tool interface for making HTTP requests
type HTTPFetcherTool struct {
	common.BuiltInTool
}

func (t HTTPFetcherTool) Name() string {
	return "http_fetcher"
}

func (t HTTPFetcherTool) Annotations() mcp.ToolAnnotation {
	return mcp.ToolAnnotation{
		Title:           "Fetch HTTP Endpoint",
		ReadOnlyHint:    common.ToPtr(true),
		DestructiveHint: common.ToPtr(false),
		IdempotentHint:  common.ToPtr(true),
		OpenWorldHint:   common.ToPtr(true),
	}
}

func (t HTTPFetcherTool) Description() string {
	return "Make HTTP GET requests to fetch content from URLs. Input should be a valid URL."
}

func (t HTTPFetcherTool) Call(ctx context.Context, input string) (string, error) {
	// #nosec G107 - HTTP requests with variable URLs are the intended functionality of this tool
	resp, err := http.Get(input)
	if err != nil {
		return "", fmt.Errorf("failed to fetch URL %s: %w", input, err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP request failed with status: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	var output string
	// Limit response size to avoid overwhelming the context
	if len(body) > 5000 {
		output = fmt.Sprintf("Content (first 5000 chars): %s...\n[Content truncated]", string(body[:5000]))
	} else {
		output = string(body)
		output += "\n"
	}

	return output, nil
}
