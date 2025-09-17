// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package logging

import (
	"context"

	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/schema"
)

// ChainedHandler forwards calls to multiple callbacks.Handler in order.
type ChainedHandler struct {
	handlers []callbacks.Handler
}

// NewChainedHandler creates a new ChainedHandler with the provided handlers.
func NewChainedHandler(handlers ...callbacks.Handler) callbacks.Handler {
	return &ChainedHandler{handlers: handlers}
}

func (c *ChainedHandler) HandleText(ctx context.Context, text string) {
	for _, h := range c.handlers {
		h.HandleText(ctx, text)
	}
}

func (c *ChainedHandler) HandleLLMStart(ctx context.Context, prompts []string) {
	for _, h := range c.handlers {
		h.HandleLLMStart(ctx, prompts)
	}
}

func (c *ChainedHandler) HandleLLMGenerateContentStart(ctx context.Context, ms []llms.MessageContent) {
	for _, h := range c.handlers {
		h.HandleLLMGenerateContentStart(ctx, ms)
	}
}

func (c *ChainedHandler) HandleLLMGenerateContentEnd(ctx context.Context, res *llms.ContentResponse) {
	for _, h := range c.handlers {
		h.HandleLLMGenerateContentEnd(ctx, res)
	}
}

func (c *ChainedHandler) HandleLLMError(ctx context.Context, err error) {
	for _, h := range c.handlers {
		h.HandleLLMError(ctx, err)
	}
}

func (c *ChainedHandler) HandleChainStart(ctx context.Context, inputs map[string]any) {
	for _, h := range c.handlers {
		h.HandleChainStart(ctx, inputs)
	}
}

func (c *ChainedHandler) HandleChainEnd(ctx context.Context, outputs map[string]any) {
	for _, h := range c.handlers {
		h.HandleChainEnd(ctx, outputs)
	}
}

func (c *ChainedHandler) HandleChainError(ctx context.Context, err error) {
	for _, h := range c.handlers {
		h.HandleChainError(ctx, err)
	}
}

func (c *ChainedHandler) HandleToolStart(ctx context.Context, input string) {
	for _, h := range c.handlers {
		h.HandleToolStart(ctx, input)
	}
}

func (c *ChainedHandler) HandleToolEnd(ctx context.Context, output string) {
	for _, h := range c.handlers {
		h.HandleToolEnd(ctx, output)
	}
}

func (c *ChainedHandler) HandleToolError(ctx context.Context, err error) {
	for _, h := range c.handlers {
		h.HandleToolError(ctx, err)
	}
}

func (c *ChainedHandler) HandleAgentAction(ctx context.Context, action schema.AgentAction) {
	for _, h := range c.handlers {
		h.HandleAgentAction(ctx, action)
	}
}

func (c *ChainedHandler) HandleAgentFinish(ctx context.Context, finish schema.AgentFinish) {
	for _, h := range c.handlers {
		h.HandleAgentFinish(ctx, finish)
	}
}

func (c *ChainedHandler) HandleRetrieverStart(ctx context.Context, query string) {
	for _, h := range c.handlers {
		h.HandleRetrieverStart(ctx, query)
	}
}

func (c *ChainedHandler) HandleRetrieverEnd(ctx context.Context, query string, documents []schema.Document) {
	for _, h := range c.handlers {
		h.HandleRetrieverEnd(ctx, query, documents)
	}
}

func (c *ChainedHandler) HandleStreamingFunc(ctx context.Context, chunk []byte) {
	for _, h := range c.handlers {
		h.HandleStreamingFunc(ctx, chunk)
	}
}
