package main

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	// Create a new MCP server
	s := server.NewMCPServer(
		"Hello Server 🚀",
		"1.0.0",
		server.WithToolCapabilities(false),
	)
	s.EnableSampling()

	// Define the tool
	tool := mcp.NewTool(
		"hello_world",
		mcp.WithDescription("Say hello to someone"),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Name of the person to greet"),
		),
	)

	// Register the tool handler
	s.AddTool(tool, helloHandler)

	// Start the server using stdio transport
	if err := server.ServeStdio(s); err != nil {
		fmt.Printf("Server error: %v\n", err)
	}
}

// Tool handler function
func helloHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Get the client session from context
	session := server.ClientSessionFromContext(ctx)
	if session == nil {
		// If no session, fall back to simple greeting
		return mcp.NewToolResultText(fmt.Sprintf("This is the MCP tool - Helloooo, %s!", name)), nil
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
							Text: fmt.Sprintf("Please provide a creative and enthusiastic greeting for %s. Make it feel that it is from someone mysterious and a little scary!", name),
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
			return mcp.NewToolResultText(fmt.Sprintf("This is the MCP tool - Helloooo, %s! (sampling failed: %v)", name, err)), nil
		}

		// Extract the generated greeting from the sampling response
		var generatedGreeting string
		if samplingResponse != nil {
			// The response Content field contains the message content
			if textContent, ok := samplingResponse.Content.(mcp.TextContent); ok {
				generatedGreeting = textContent.Text
			} else if contentStr, ok := samplingResponse.Content.(string); ok {
				generatedGreeting = contentStr
			}
		}

		// If we got a response, use it
		if generatedGreeting != "" {
			return mcp.NewToolResultText(fmt.Sprintf("🤖 AI-Generated Greeting: %s", generatedGreeting)), nil
		}
	}

	// Fallback to simple greeting
	return mcp.NewToolResultText(fmt.Sprintf("This is the MCP tool - Helloooo, %s!", name)), nil
}
