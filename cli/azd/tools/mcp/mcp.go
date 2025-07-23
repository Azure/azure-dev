package main

import (
	"context"
	"fmt"
	"log"

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
		"Error_Handler",
		mcp.WithDescription("Explain the error and provide a suggestion to fix it for azd provision related errors"),
		mcp.WithString("errorPromptProvision",
			mcp.Required(),
			mcp.Description("Complete error message from 'azd provision' command failure and complete prompt request to the tool"),
		),
	)

	// Register the tool handler
	s.AddTool(tool, errorProvisionHandler)

	// Start the server using stdio transport
	if err := server.ServeStdio(s); err != nil {
		fmt.Printf("Server error: %v\n", err)
	}
}

func errorProvisionHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log.Println("🤖 Starting errorProvisionHandler session...")
	promptRequest, err := request.RequireString("errorPromptProvision")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Get the client session from context
	session := server.ClientSessionFromContext(ctx)
	if session == nil {
		// If no session, return to basic error
		return mcp.NewToolResultText(fmt.Sprintf("Failed to connect MCP tool, fallback to original prompt request: %s", promptRequest)), nil
	}

	// For VSCode only agent tool, this change will included in a separate PR for MCP tool
	// 	samplingText := fmt.Sprintf(`
	// 	I'm using Azure Developer CLI (azd) and running command 'azd provision'. I encountered the following error: %s

	// Determine if this error is a azure related error or http response error or authentication error or other errors. Please:

	// 1. Explain what this specific error means and why it occurred
	// 2. Provide step-by-step troubleshooting instructions
	// 3. If it is a azure related error or http response error, check infra folder and suggest specific fixes for Bicep files or Terraform files based on files in infra folder. After that, if user has azure cli installed, provide the exact Azure CLI commands and azure portal instructions to verify the changes works
	// 4. Include any relevant azure.yaml configuration changes that might be needed

	// Focus on actionable solutions rather than general advice.
	// 	`, promptRequest)
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
							Text: fmt.Sprintf("I'm running azd command 'azd provision'. %s", promptRequest),
						},
					},
				},
				MaxTokens:   100,
				Temperature: 0.8,
			},
		}

		log.Printf("🤖 Sampling Request: %+v\n", samplingRequest)

		// Send the sampling request to get a response from the host's LLM
		samplingResponse, err := samplingSession.RequestSampling(ctx, samplingRequest)
		log.Printf("🤖 Sampling Response: %+v\n", samplingResponse)
		if err != nil {
			// If sampling fails, fall back to a simple greeting
			return mcp.NewToolResultText(fmt.Sprintf("Failed to send sampling request, fallback to original prompt request: %s", promptRequest)), nil
		}

		// Extract the generated greeting from the sampling response
		var errorSuggestion string
		if samplingResponse != nil {
			// The response Content field contains the message content
			if textContent, ok := samplingResponse.Content.(mcp.TextContent); ok {
				errorSuggestion = textContent.Text
			} else if contentStr, ok := samplingResponse.Content.(string); ok {
				errorSuggestion = contentStr
			}
		}

		// If we got a response, use it
		if errorSuggestion != "" {
			return mcp.NewToolResultText(fmt.Sprintf("🤖 AI-Generated Error Suggestion: %s", errorSuggestion)), nil
		}
	}

	// Fallback to raw error message
	return mcp.NewToolResultText(fmt.Sprintf("Failed to generate error suggestions, fallback to original prompt request: %s", promptRequest)), nil
}
