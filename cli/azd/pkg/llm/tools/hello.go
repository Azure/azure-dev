package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Hello tool is an MCP server tool that greets users.
// It is used as an example of how to create a tool that can interact with users.
// It supports sampling to generate creative greetings.
func NewHello() server.ServerTool {
	tool := mcp.NewTool(
		"hello_world",
		mcp.WithDescription("Say hello to someone"),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Name of the person to greet"),
		),
	)
	return server.ServerTool{
		Tool:    tool,
		Handler: helloHandler,
	}
}

func helloHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Get the client session from context
	session := server.ClientSessionFromContext(ctx)
	if session == nil {
		// If no session, fall back to simple greeting
		return mcp.NewToolResultText(fmt.Sprintf("We are here...., %s!", name)), nil
	}

	// Check if the session supports sampling
	if samplingSession, ok := session.(server.SessionWithSampling); ok {
		// Create a sampling request to get a creative greeting
		samplingRequest := mcp.CreateMessageRequest{
			CreateMessageParams: mcp.CreateMessageParams{
				Messages: []mcp.SamplingMessage{
					{
						Role: mcp.RoleUser,
						Content: mcp.TextContent{
							Type: "text",
							Text: fmt.Sprintf("Make a poem with the name of  %s. Make it scary and short", name),
						},
					},
				},
				MaxTokens:   100,
				Temperature: 0.8,
			},
		}

		// Send the sampling request to get a response from the host's LLM
		samplingResponse, err := samplingSession.RequestSampling(ctx, samplingRequest)
		if err != nil {
			// If sampling fails, fall back to a simple greeting
			return mcp.NewToolResultText(
				fmt.Sprintf("This is the MCP tool - Hello, %s! (sampling failed: %v)", name, err)), nil
		}

		// Extract the generated greeting from the sampling response
		var generatedGreeting string
		if samplingResponse != nil {
			// The response Content field contains the message content
			if textContent, ok := samplingResponse.Content.(mcp.TextContent); ok {
				generatedGreeting = textContent.Text
			} else if contentStr, ok := samplingResponse.Content.(string); ok {
				generatedGreeting = contentStr
			} else if bytes, err := json.Marshal(samplingResponse.Content); err == nil {
				var msg mcp.TextContent
				err = json.Unmarshal(bytes, &msg)
				if err == nil {
					generatedGreeting = msg.Text
				} else {
					generatedGreeting = fmt.Sprintf("Error parsing content: %v", err)
				}
			} else {
				generatedGreeting = fmt.Sprintf("%v", samplingResponse.Content)
			}
		}

		// If we got a response, use it
		if generatedGreeting != "" {
			return mcp.NewToolResultText(fmt.Sprintf("🤖 AI-Generated Greeting: %s", generatedGreeting)), nil
		}
	}

	// Fallback to simple greeting
	return mcp.NewToolResultText(fmt.Sprintf("This is the MCP tool - Hello, %s!", name)), nil
}
