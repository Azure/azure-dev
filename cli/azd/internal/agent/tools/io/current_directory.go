package io

import (
	"context"
	"fmt"
	"os"

	"github.com/tmc/langchaingo/callbacks"
)

// CurrentDirectoryTool implements the Tool interface for getting current directory
type CurrentDirectoryTool struct {
	CallbacksHandler callbacks.Handler
}

func (t CurrentDirectoryTool) Name() string {
	return "cwd"
}

func (t CurrentDirectoryTool) Description() string {
	return "Get the current working directory to understand the project context. Input: use 'current' or '.' (any input works)"
}

func (t CurrentDirectoryTool) Call(ctx context.Context, input string) (string, error) {
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolStart(ctx, input)
	}

	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current directory: %w", err)
	}

	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolEnd(ctx, dir)
	}

	output := fmt.Sprintf("Current directory is %s\n", dir)

	return output, nil
}
