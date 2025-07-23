// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agentRunner

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/llm"
	langchaingo_mcp_adapter "github.com/i2y/langchaingo-mcp-adapter"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/chains"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/schema"
)

type samplingHandler struct {
	llmClient llms.Model
	console   input.Console
}

func (s *samplingHandler) CreateMessage(
	ctx context.Context, request mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
	// Enhanced logging for debugging
	log.Printf("🔬 MCP Sampling Request received!\n")
	log.Printf("   Request ID: %v\n", ctx.Value("requestId"))
	log.Printf("   Max tokens: %d\n", request.MaxTokens)
	log.Printf("   Temperature: %f\n", request.Temperature)
	log.Printf("   Model preferences: %v\n", request.ModelPreferences)
	log.Printf("   Number of messages: %d\n", len(request.Messages))

	// Debug: Print message details
	for i, msg := range request.Messages {
		log.Printf("   Message %d: Role=%s, Content=%v\n", i, msg.Role, msg.Content)
	}

	// Convert MCP messages to LLM format
	var llmMessages []llms.MessageContent
	for _, msg := range request.Messages {
		var content []llms.ContentPart

		// Handle the Content field which can be different types
		switch contentType := msg.Content.(type) {
		case mcp.TextContent:
			log.Printf("   Processing TextContent: %s\n", contentType.Text)
			content = append(content, llms.TextPart(contentType.Text))
		case string:
			log.Printf("   Processing string content: %s\n", contentType)
			content = append(content, llms.TextPart(contentType))
		default:
			// Try to convert to string as fallback
			contentStr := fmt.Sprintf("%v", msg.Content)
			log.Printf("   Processing unknown content type: %s\n", contentStr)
			content = append(content, llms.TextPart(contentStr))
		}

		// Map MCP roles to LLM roles
		var role llms.ChatMessageType
		switch msg.Role {
		case mcp.RoleUser:
			role = llms.ChatMessageTypeHuman
		case mcp.RoleAssistant:
			role = llms.ChatMessageTypeAI
		default:
			role = llms.ChatMessageTypeSystem
		}

		llmMessages = append(llmMessages, llms.MessageContent{
			Role:  role,
			Parts: content,
		})
	}

	// Generate response using the LLM
	log.Printf("🧠 Generating response with LLM (messages: %d)...\n", len(llmMessages))
	response, err := s.llmClient.GenerateContent(ctx, llmMessages)
	if err != nil {
		log.Printf("❌ LLM generation error: %v\n", err)
		return nil, fmt.Errorf("failed to generate LLM response: %w", err)
	}

	// Extract text from the response
	var responseText string
	if len(response.Choices) > 0 && len(response.Choices[0].Content) > 0 {
		// Convert the response content to string
		responseText = string(response.Choices[0].Content)
		log.Printf("📝 Raw LLM response: %s\n", responseText)
	}

	if responseText == "" {
		responseText = "No response generated"
		log.Printf("⚠️  Using fallback response\n")
	}

	log.Printf("✅ LLM response generated (length: %d): %s\n", len(responseText), responseText[:min(100, len(responseText))])

	// Return the MCP result using the same format as the MCP server
	result := &mcp.CreateMessageResult{
		SamplingMessage: mcp.SamplingMessage{
			Role:    mcp.RoleAssistant,
			Content: responseText,
		},
		Model: "llm-delegated",
	}

	log.Printf("🎯 Returning sampling result with model: %s\n", result.Model)
	return result, nil
}

// Helper function for min
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func Run(ctx context.Context, console input.Console, llmManager llm.Manager, errNeedSuggestion error) (string, error) {
	llmInfo, err := llmManager.Info(console.GetWriter())
	if err != nil {
		return "", fmt.Errorf("failed to load LLM info: %w", err)
	}
	llClient, err := llm.LlmClient(llmInfo)
	if err != nil {
		return "", fmt.Errorf("failed to create LLM client: %w", err)
	}

	// Create a callback handler to log agent steps
	callbackHandler := &agentLogHandler{console: console}

	// defer mcpClient.Close()
	t := transport.NewStdioWithOptions("C:\\Users\\hemarina\\Downloads\\vhvb1989\\azure-dev\\cli\\azd\\tools\\mcp\\mcp.exe", nil, nil)
	// Create sampling handler with LLM client
	samplingHandler := &samplingHandler{
		llmClient: llClient,
		console:   console,
	}

	mcpClient := client.NewClient(t, client.WithSamplingHandler(samplingHandler))
	if err := mcpClient.Start(ctx); err != nil {
		return "", fmt.Errorf("failed to start MCP client: %w", err)
	}
	defer mcpClient.Close()

	log.Println("🔌 MCP client created with sampling handler")

	// Create adapter
	adapter, err := langchaingo_mcp_adapter.New(mcpClient)

	if err != nil {
		log.Fatalf("Failed to create adapter: %v", err)
	}

	// Load tools from MCP server
	tools, err := adapter.Tools()
	if err != nil {
		log.Fatalf("Failed to get tools: %v", err)
	}

	agent := agents.NewOneShotAgent(llClient, tools, agents.WithCallbacksHandler(callbackHandler))

	executor := agents.NewExecutor(agent)

	log.Println("🤖 Starting AI agent execution...")
	log.Printf("   Agent has %d tools available from MCP server\n", len(tools))
	log.Println("   Sampling handler is configured for MCP tool requests")

	// ask the agent to describe
	// instructions to the error
	input := promptingWithDifferentErrors(errNeedSuggestion)

	answer, err := chains.Run(ctx, executor, input,
		chains.WithTemperature(0.0),
	)
	if err != nil {
		return "", fmt.Errorf("failed to exe: %w", err)
	}
	log.Println("✅ AI agent execution completed")

	return answer, nil
}

// agentLogHandler implements callbacks.Handler to log agent execution steps
type agentLogHandler struct {
	console input.Console
	step    int
}

// HandleLLMGenerateContentStart implements callbacks.Handler.
func (h *agentLogHandler) HandleLLMGenerateContentStart(ctx context.Context, ms []llms.MessageContent) {
}

// HandleRetrieverEnd implements callbacks.Handler.
func (h *agentLogHandler) HandleRetrieverEnd(ctx context.Context, query string, documents []schema.Document) {
}

// HandleRetrieverStart implements callbacks.Handler.
func (h *agentLogHandler) HandleRetrieverStart(ctx context.Context, query string) {
}

// HandleStreamingFunc implements callbacks.Handler.
func (h *agentLogHandler) HandleStreamingFunc(ctx context.Context, chunk []byte) {
	// use console to stream output
	if len(chunk) > 0 {
		// Print the chunk to the console
		log.Print(string(chunk))
	}
}

func (h *agentLogHandler) HandleLLMStart(ctx context.Context, prompts []string) {
	h.step++
	log.Printf("🧠 Step %d: LLM processing...\n", h.step)
	if len(prompts) > 0 && len(prompts[0]) < 200 {
		log.Printf("   Prompt: %s\n", prompts[0])
	}
}

func (h *agentLogHandler) HandleLLMError(ctx context.Context, err error) {
	log.Printf("❌ Step %d: LLM error: %v\n", h.step, err)
}

func (h *agentLogHandler) HandleChainStart(ctx context.Context, inputs map[string]any) {
	log.Println("🚀 Agent chain started")
}

func (h *agentLogHandler) HandleChainEnd(ctx context.Context, outputs map[string]any) {
	log.Println("🏁 Agent chain completed")
}

func (h *agentLogHandler) HandleChainError(ctx context.Context, err error) {
	log.Printf("💥 Agent chain error: %v\n", err)
}

func (h *agentLogHandler) HandleToolStart(ctx context.Context, input string) {
	log.Printf("🔧 Using tool with input: %s\n", input)
	if input != "" && len(input) < 100 {
		log.Printf("   Input: %s\n", input)
	}
}

func (h *agentLogHandler) HandleToolEnd(ctx context.Context, output string) {
	if output != "" && len(output) < 150 {
		log.Printf("   Output: %s\n", output)
	} else {
		log.Println("   Tool completed")
	}
}

func (h *agentLogHandler) HandleToolError(ctx context.Context, err error) {
	log.Printf("   ❌ Tool error: %v\n", err)
}

func (h *agentLogHandler) HandleText(ctx context.Context, text string) {
	if text != "" && len(text) < 200 {
		log.Printf("💭 Agent thinking: %s\n", text)
	}
}

func (h *agentLogHandler) HandleAgentAction(ctx context.Context, action schema.AgentAction) {
	log.Printf("🎯 Agent action: %s\n", action.Tool)
	if action.ToolInput != "" && len(action.ToolInput) < 100 {
		log.Printf("   Tool input: %s\n", action.ToolInput)
	}
}

func (h *agentLogHandler) HandleAgentFinish(ctx context.Context, finish schema.AgentFinish) {
	log.Println("🏆 Agent finished successfully")
	if finish.ReturnValues != nil {
		log.Printf("   Final output: %v\n", finish.ReturnValues)
	}
}

func (h *agentLogHandler) HandleLLMGenerateContentEnd(ctx context.Context, response *llms.ContentResponse) {
	log.Println("✨ LLM content generation completed")
}

func promptingWithDifferentErrors(err error) string {
	var respErr *azcore.ResponseError
	var armDeployErr *azapi.AzureDeploymentError
	var authFailedErr *auth.AuthFailedError
	if errors.As(err, &respErr) {
		return fmt.Sprintf(`I'm using Azure Developer CLI (azd) and encountered an Azure HTTP response error: %s

This appears to be an Azure REST API error with status code %d and error code '%s'. Please:

1. Explain what this specific error means and why it occurred
2. Provide step-by-step troubleshooting instructions without az cli command and instructions with az cli command
3. Suggest specific fixes for Bicep files and Terraform files if this is infrastructure provisioning related
4. If this involves Azure resource permissions, quotas, or configuration issues, provide the exact azure portal instructions and az cli commands to verify the changes from bicep or terraform files works 
5. Provide suggestions only if this requires changes to Azure subscription settings, resource group permissions, or service principal setup

Focus on actionable solutions rather than general advice.`,
			err.Error(), respErr.StatusCode, respErr.ErrorCode)
	} else if errors.As(err, &armDeployErr) {
		return fmt.Sprintf(`I'm using Azure Developer CLI (azd) and encountered an Azure deployment error: %s

This is a deployment validation or provisioning failure. Please:

1. Explain what this specific error means and why it occurred
2. Provide step-by-step troubleshooting instructions  without az cli command and instructions with az cli command
3. Suggest specific fixes for Bicep files and Terraform files
4. Provide the exact azure portal instructions and az cli commands to verify the suggested changes from bicep or terraform files works

Focus on actionable solutions rather than general advice.`,
			err.Error())
	} else if errors.As(err, &authFailedErr) {
		// We should move this part under azd auth command
		return fmt.Sprintf(`I'm using Azure Developer CLI (azd) and encountered an authentication error: %s. Please:

1. Explain what this specific Azure authentication error means and common causes.
2. Identify which auth method is failing (device code, service principal, managed identity, interactive) and what should I do to fix it.
3. Provide specific azd auth commands to re-authenticate:
   - azd auth logout
   - azd auth login
4. Ensure correct tenant and subscription are selected
5. Verify Azure-related environment variables are correct

Focus on actionable solutions rather than general advice.`, err.Error())
	}

	return fmt.Sprintf("I'm using Azure Developer CLI (azd) and I encountered an error: %s. Explain the error and what should I do next to fix it. Focus on actionable solutions rather than general advice.", err.Error())
}
