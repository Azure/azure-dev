// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func NewSamplingTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"azd_sample_test",
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription("Runs MCP sampling to test sampling behavior"),
		),
		Handler: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			serverFromCtx := server.ServerFromContext(ctx)

			elicitationRequest := mcp.ElicitationRequest{
				Params: mcp.ElicitationParams{
					Message: "Can we learn some more information about you?",
					RequestedSchema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name": map[string]any{
								"type":        "string",
								"description": "The name of the user",
							},
							"language": map[string]any{
								"type":        "string",
								"description": "Favorite programming language",
								"enum":        []string{"Go", "Python", "JavaScript", "TypeScript", "Java", "C#", "C++"},
							},
							"team": map[string]any{
								"type":        "string",
								"description": "The team that you are a member of",
							},
						},
						"required": []string{"name"},
					},
				},
			}
			elicitationResult, err := serverFromCtx.RequestElicitation(ctx, elicitationRequest)
			if err != nil {
				return mcp.NewToolResultErrorFromErr("Error during elicitation", err), nil
			}

			samplingRequest := mcp.CreateMessageRequest{
				CreateMessageParams: mcp.CreateMessageParams{
					Messages: []mcp.SamplingMessage{
						{
							Role: mcp.RoleUser,
							Content: mcp.TextContent{
								Type: "text",
								Text: "What is 10 plus 10?",
							},
						},
					},
					SystemPrompt: "You are a helpful assistant",
					MaxTokens:    1000,
					Temperature:  0.7,
				},
			}

			samplingResult, err := serverFromCtx.RequestSampling(ctx, samplingRequest)
			if err != nil {
				return mcp.NewToolResultErrorFromErr("Error during sampling", err), nil
			}

			if textContent, ok := samplingResult.Content.(mcp.TextContent); ok {
				return mcp.NewToolResultText(textContent.Text), nil
			}

			return mcp.NewToolResultText(
				fmt.Sprintf("Sampling: %v, Elicitation: %v", samplingResult.Content, elicitationResult.Content),
			), nil
		},
	}
}
