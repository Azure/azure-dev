package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// HTTPFetcherTool implements the Tool interface for making HTTP requests
type HTTPFetcherTool struct{}

func (t HTTPFetcherTool) Name() string {
	return "http_fetcher"
}

func (t HTTPFetcherTool) Description() string {
	return "Make HTTP GET requests to fetch content from URLs. Input should be a valid URL."
}

func (t HTTPFetcherTool) Call(ctx context.Context, input string) (string, error) {
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

	// Limit response size to avoid overwhelming the context
	if len(body) > 5000 {
		return fmt.Sprintf("Content (first 5000 chars): %s...\n[Content truncated]", string(body[:5000])), nil
	}

	return string(body), nil
}
