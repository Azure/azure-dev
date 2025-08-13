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

			return mcp.NewToolResultText(fmt.Sprintf("%v", samplingResult.Content)), nil
		},
	}
}
