// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fatih/color"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tmc/langchaingo/llms"
)

type McpSamplingHandler struct {
	llm   llms.Model
	debug bool
}

type SamplingHandlerOption func(*McpSamplingHandler)

func WithDebug(debug bool) SamplingHandlerOption {
	return func(h *McpSamplingHandler) {
		h.debug = debug
	}
}

func NewMcpSamplingHandler(llm llms.Model, opts ...SamplingHandlerOption) *McpSamplingHandler {
	handler := &McpSamplingHandler{
		llm: llm,
	}

	for _, opt := range opts {
		opt(handler)
	}

	return handler
}

// cleanContent converts literal line break escape sequences to actual line break characters
func (h *McpSamplingHandler) cleanContent(content string) string {
	// Replace literal escape sequences with actual control characters
	// Handle Windows-style \r\n first (most common), then individual ones
	content = strings.ReplaceAll(content, "\\r\\n", "\r\n")
	content = strings.ReplaceAll(content, "\\n", "\n")
	content = strings.ReplaceAll(content, "\\r", "\r")
	return content
}

func (h *McpSamplingHandler) CreateMessage(
	ctx context.Context,
	request mcp.CreateMessageRequest,
) (*mcp.CreateMessageResult, error) {
	if h.debug {
		requestJson, err := json.MarshalIndent(request, "", "  ")
		if err != nil {
			return nil, err
		}

		color.HiBlack("\nSamplingStart\n%s\n", requestJson)
	}

	messages := []llms.MessageContent{}
	for _, msg := range request.Messages {
		var parts []llms.ContentPart

		switch content := msg.Content.(type) {
		case mcp.TextContent:
			parts = append(parts, llms.TextPart(h.cleanContent(content.Text)))
		case string:
			// Simple text content
			parts = append(parts, llms.TextPart(h.cleanContent(content)))
		case map[string]interface{}:
			// Map content - convert each key/value pair to text content
			for key, value := range content {
				if key == "text" {
					parts = append(parts, llms.TextPart(h.cleanContent(fmt.Sprintf("%v", value))))
					break
				}
			}
		case []interface{}:
			// Array of content parts (could be text, images, etc.)
			for _, part := range content {
				if textPart, ok := part.(string); ok {
					parts = append(parts, llms.TextPart(h.cleanContent(textPart)))
				}
			}

		default:
			// Fallback: convert to string
			parts = append(parts, llms.TextPart(h.cleanContent(fmt.Sprintf("%v", content))))
		}

		messages = append(messages, llms.MessageContent{
			Role:  llms.ChatMessageTypeAI,
			Parts: parts,
		})
	}

	if h.debug {
		inputJson, err := json.MarshalIndent(messages, "", "  ")
		if err != nil {
			return nil, err
		}

		color.HiBlack("\nSamplingLLMContent\n%s\n", inputJson)
	}

	res, err := h.llm.GenerateContent(ctx, messages)
	if err != nil {
		return &mcp.CreateMessageResult{
			SamplingMessage: mcp.SamplingMessage{
				Role:    mcp.RoleAssistant,
				Content: llms.TextPart(err.Error()),
			},
			Model:      "llm-delegated",
			StopReason: "error",
		}, nil
	}

	var samplingResponse *mcp.CreateMessageResult

	if len(res.Choices) == 0 {
		samplingResponse = &mcp.CreateMessageResult{
			SamplingMessage: mcp.SamplingMessage{
				Role:    mcp.RoleAssistant,
				Content: llms.TextPart(""),
			},
			Model:      "llm-delegated",
			StopReason: "no_choices",
		}
	} else {
		// Use the first choice
		choice := res.Choices[0]

		samplingResponse = &mcp.CreateMessageResult{
			SamplingMessage: mcp.SamplingMessage{
				Role:    mcp.RoleAssistant,
				Content: llms.TextPart(choice.Content),
			},
			Model:      "llm-delegated",
			StopReason: "endTurn",
		}
	}

	if h.debug {
		responseJson, err := json.MarshalIndent(samplingResponse, "", "  ")
		if err != nil {
			return nil, err
		}

		color.HiBlack("\nSamplingEnd\n%s\n", responseJson)
	}

	return samplingResponse, nil
}
