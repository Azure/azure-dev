package mcp

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tmc/langchaingo/llms"
)

type McpSamplingHandler struct {
	llm llms.Model
}

func NewMcpSamplingHandler(llm llms.Model) *McpSamplingHandler {
	return &McpSamplingHandler{
		llm: llm,
	}
}

func (h *McpSamplingHandler) CreateMessage(ctx context.Context, request mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
	messages := []llms.MessageContent{}
	for _, inputMessage := range request.Messages {
		// Map MCP Role to langchaingo ChatMessageType
		var chatMessageType llms.ChatMessageType
		switch inputMessage.Role {
		case mcp.RoleAssistant:
			chatMessageType = llms.ChatMessageTypeAI
		case mcp.RoleUser:
			chatMessageType = llms.ChatMessageTypeHuman
		default:
			// Fallback for unknown roles
			chatMessageType = llms.ChatMessageTypeHuman
		}

		// Handle Content field - it's defined as 'any' in MCP SamplingMessage
		var parts []llms.ContentPart
		switch content := inputMessage.Content.(type) {
		case string:
			// Simple text content
			parts = []llms.ContentPart{
				llms.TextContent{
					Text: content,
				},
			}
		case []interface{}:
			// Array of content parts (could be text, images, etc.)
			for _, part := range content {
				if textPart, ok := part.(string); ok {
					parts = append(parts, llms.TextContent{
						Text: textPart,
					})
				}
				// Could add support for other content types here (images, etc.)
			}
		case map[string]interface{}:
			// Map content - convert each key/value pair to text content
			for key, value := range content {
				parts = append(parts, llms.TextContent{
					Text: fmt.Sprintf("%s: %v", key, value),
				})
			}
		default:
			// Fallback: convert to string
			parts = []llms.ContentPart{
				llms.TextContent{
					Text: fmt.Sprintf("%v", content),
				},
			}
		}

		messages = append(messages, llms.MessageContent{
			Role:  chatMessageType,
			Parts: parts,
		})
	}

	res, err := h.llm.GenerateContent(ctx, messages)
	if err != nil {
		return nil, err
	}

	// Transform langchaingo response back to MCP format
	// Get model name from hints if available
	modelName := ""
	if request.ModelPreferences != nil && len(request.ModelPreferences.Hints) > 0 {
		modelName = request.ModelPreferences.Hints[0].Name
	}

	if len(res.Choices) == 0 {
		return &mcp.CreateMessageResult{
			SamplingMessage: mcp.SamplingMessage{
				Role:    mcp.RoleAssistant,
				Content: "",
			},
			Model:      modelName,
			StopReason: "no_choices",
		}, nil
	}

	// Use the first choice
	choice := res.Choices[0]

	return &mcp.CreateMessageResult{
		SamplingMessage: mcp.SamplingMessage{
			Role:    mcp.RoleAssistant,
			Content: choice.Content,
		},
		Model: modelName,
	}, nil
}
