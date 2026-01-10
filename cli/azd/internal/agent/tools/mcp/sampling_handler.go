// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/agent/consent"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/llm"
	"github.com/fatih/color"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tmc/langchaingo/llms"
)

// McpSamplingHandler handles sampling requests from MCP clients by delegating
// to an underlying language model and converting responses to MCP format
type McpSamplingHandler struct {
	llm            *llm.ModelContainer
	debug          bool
	consentManager consent.ConsentManager
	console        input.Console
}

// SamplingHandlerOption is a functional option for configuring McpSamplingHandler
type SamplingHandlerOption func(*McpSamplingHandler)

// WithDebug returns an option that enables or disables debug logging
func WithDebug(debug bool) SamplingHandlerOption {
	return func(h *McpSamplingHandler) {
		h.debug = debug
	}
}

// NewMcpSamplingHandler creates a new MCP sampling handler with the specified
// language model and applies any provided options
func NewMcpSamplingHandler(
	consentManager consent.ConsentManager,
	console input.Console,
	llm *llm.ModelContainer,
	opts ...SamplingHandlerOption,
) client.SamplingHandler {
	handler := &McpSamplingHandler{
		consentManager: consentManager,
		console:        console,
		llm:            llm,
	}

	for _, opt := range opts {
		opt(handler)
	}

	return handler
}

// CreateMessage handles MCP sampling requests by converting MCP messages to the
// language model format, generating a response, and converting back to MCP format.
// It supports various content types including text, maps, and arrays, and provides
// debug logging when enabled. Returns an error-wrapped response if LLM generation fails.
func (h *McpSamplingHandler) CreateMessage(
	ctx context.Context,
	request mcp.CreateMessageRequest,
) (*mcp.CreateMessageResult, error) {
	// Get current executing tool for context (package-level tracking)
	currentTool := consent.GetCurrentExecutingTool()
	if currentTool == nil {
		return nil, fmt.Errorf("no current tool executing")
	}

	// Check consent for sampling if consent manager is available
	if err := h.checkSamplingConsent(ctx, currentTool, request); err != nil {
		return &mcp.CreateMessageResult{
			SamplingMessage: mcp.SamplingMessage{
				Role:    mcp.RoleAssistant,
				Content: llms.TextPart(fmt.Sprintf("Sampling request denied: %v", err)),
			},
			Model:      "consent-denied",
			StopReason: "consent_denied",
		}, nil
	}

	if h.debug {
		requestJson, err := json.MarshalIndent(request, "", "  ")
		if err != nil {
			return nil, err
		}

		color.HiBlack("\nSamplingStart (Tool: %s/%s)\n%s\n", currentTool.Server, currentTool.Name, requestJson)
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

	res, err := h.llm.Model.GenerateContent(ctx, messages)
	if err != nil {
		return &mcp.CreateMessageResult{
			SamplingMessage: mcp.SamplingMessage{
				Role:    mcp.RoleAssistant,
				Content: llms.TextPart(err.Error()),
			},
			Model:      h.llm.Metadata.Name,
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
			Model:      h.llm.Metadata.Name,
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
			Model:      h.llm.Metadata.Name,
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

// cleanContent converts literal line break escape sequences to actual line break characters.
// It handles Windows-style \r\n sequences first, then individual \n and \r sequences.
func (h *McpSamplingHandler) cleanContent(content string) string {
	// Replace literal escape sequences with actual control characters
	// Handle Windows-style \r\n first (most common), then individual ones
	content = strings.ReplaceAll(content, "\\r\\n", "\r\n")
	content = strings.ReplaceAll(content, "\\n", "\n")
	content = strings.ReplaceAll(content, "\\r", "\r")
	return content
}

// checkSamplingConsent checks consent for sampling requests using the current executing tool
func (h *McpSamplingHandler) checkSamplingConsent(
	ctx context.Context,
	currentTool *consent.ExecutingTool,
	request mcp.CreateMessageRequest,
) error {
	// Create a consent checker for this specific server
	consentChecker := consent.NewConsentChecker(h.consentManager, currentTool.Server)

	// Check sampling consent using the consent checker
	decision, err := consentChecker.CheckSamplingConsent(ctx, currentTool.Name)
	if err != nil {
		return fmt.Errorf("consent check failed: %w", err)
	}

	if !decision.Allowed {
		if decision.RequiresPrompt {
			// Use console.DoInteraction to show consent prompt
			if err := h.console.DoInteraction(func() error {
				return consentChecker.PromptAndGrantSamplingConsent(
					ctx,
					currentTool.Name,
					"Allows sending data to external language models for processing",
				)
			}); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("sampling denied: %s", decision.Reason)
		}
	}

	return nil
}
