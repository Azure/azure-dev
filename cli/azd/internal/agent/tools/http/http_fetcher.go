package http

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/tmc/langchaingo/callbacks"
)

// HTTPFetcherTool implements the Tool interface for making HTTP requests
type HTTPFetcherTool struct {
	CallbacksHandler callbacks.Handler
}

func (t HTTPFetcherTool) Name() string {
	return "http_fetcher"
}

func (t HTTPFetcherTool) Description() string {
	return "Make HTTP GET requests to fetch content from URLs. Input should be a valid URL."
}

func (t HTTPFetcherTool) Call(ctx context.Context, input string) (string, error) {
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolStart(ctx, fmt.Sprintf("http_fetcher: %s", input))
	}

	resp, err := http.Get(input)
	if err != nil {
		toolErr := fmt.Errorf("failed to fetch URL %s: %w", input, err)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("HTTP request failed with status: %s", resp.Status)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, err)
		}
		return "", err
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		toolErr := fmt.Errorf("failed to read response body: %w", err)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}

	var output string
	// Limit response size to avoid overwhelming the context
	if len(body) > 5000 {
		output = fmt.Sprintf("Content (first 5000 chars): %s...\n[Content truncated]", string(body[:5000]))
	} else {
		output = string(body)
		output += "\n"
	}

	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolEnd(ctx, output)
	}

	return output, nil
}
