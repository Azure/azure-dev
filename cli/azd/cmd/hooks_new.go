// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/llm"
	langchaingo_mcp_adapter "github.com/i2y/langchaingo-mcp-adapter"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/chains"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/schema"
)

func newHooksNewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "new",
		Short: "Create a new hook for the project.",
	}
}

func newHooksNewFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *hooksNewFlags {
	flags := &hooksNewFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

type hooksNewFlags struct {
	internal.EnvFlag
	global   *internal.GlobalCommandOptions
	platform string
	service  string
}

func (f *hooksNewFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.EnvFlag.Bind(local, global)
	f.global = global

	local.StringVar(&f.platform, "platform", "", "Forces hooks to run for the specified platform.")
	local.StringVar(&f.service, "service", "", "Only runs hooks for the specified service.")
}

type hooksNewAction struct {
	commandRunner  exec.CommandRunner
	console        input.Console
	flags          *hooksNewFlags
	args           []string
	serviceLocator ioc.ServiceLocator
	llmManager     llm.Manager
}

type samplingHandler struct {
	llmClient llms.Model
	console   input.Console
}

func (s *samplingHandler) CreateMessage(
	ctx context.Context, request mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
	// Enhanced logging for debugging
	fmt.Printf("🔬 MCP Sampling Request received!\n")
	fmt.Printf("   Request ID: %v\n", ctx.Value("requestId"))
	fmt.Printf("   Max tokens: %d\n", request.MaxTokens)
	fmt.Printf("   Temperature: %f\n", request.Temperature)
	fmt.Printf("   Model preferences: %v\n", request.ModelPreferences)
	fmt.Printf("   Number of messages: %d\n", len(request.Messages))

	// Debug: Print message details
	for i, msg := range request.Messages {
		fmt.Printf("   Message %d: Role=%s, Content=%v\n", i, msg.Role, msg.Content)
	}

	// Convert MCP messages to LLM format
	var llmMessages []llms.MessageContent
	for _, msg := range request.Messages {
		var content []llms.ContentPart

		// Handle the Content field which can be different types
		switch contentType := msg.Content.(type) {
		case mcp.TextContent:
			fmt.Printf("   Processing TextContent: %s\n", contentType.Text)
			content = append(content, llms.TextPart(contentType.Text))
		case string:
			fmt.Printf("   Processing string content: %s\n", contentType)
			content = append(content, llms.TextPart(contentType))
		default:
			// Try to convert to string as fallback
			contentStr := fmt.Sprintf("%v", msg.Content)
			fmt.Printf("   Processing unknown content type: %s\n", contentStr)
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
	fmt.Printf("🧠 Generating response with LLM (messages: %d)...\n", len(llmMessages))
	response, err := s.llmClient.GenerateContent(ctx, llmMessages)
	if err != nil {
		fmt.Printf("❌ LLM generation error: %v\n", err)
		return nil, fmt.Errorf("failed to generate LLM response: %w", err)
	}

	// Extract text from the response
	var responseText string
	if len(response.Choices) > 0 && len(response.Choices[0].Content) > 0 {
		// Convert the response content to string
		responseText = string(response.Choices[0].Content)
		fmt.Printf("📝 Raw LLM response: %s\n", responseText)
	}

	if responseText == "" {
		responseText = "No response generated"
		fmt.Printf("⚠️  Using fallback response\n")
	}

	fmt.Printf("✅ LLM response generated (length: %d): %s\n", len(responseText), responseText[:min(100, len(responseText))])

	// Return the MCP result using the same format as the MCP server
	result := &mcp.CreateMessageResult{
		SamplingMessage: mcp.SamplingMessage{
			Role:    mcp.RoleAssistant,
			Content: responseText,
		},
		Model: "llm-delegated",
	}

	fmt.Printf("🎯 Returning sampling result with model: %s\n", result.Model)
	return result, nil
}

// Helper function for min
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func newHooksNewAction(
	commandRunner exec.CommandRunner,
	console input.Console,
	flags *hooksNewFlags,
	args []string,
	serviceLocator ioc.ServiceLocator,
	llmManager llm.Manager,
) actions.Action {
	return &hooksNewAction{
		commandRunner:  commandRunner,
		console:        console,
		flags:          flags,
		args:           args,
		serviceLocator: serviceLocator,
		llmManager:     llmManager,
	}
}

func (hna *hooksNewAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	llmInfo, err := hna.llmManager.Info(hna.console.GetWriter())
	if err != nil {
		return nil, fmt.Errorf("failed to load LLM info: %w", err)
	}
	llClient, err := llm.LlmClient(llmInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM client: %w", err)
	}

	// Create a callback handler to log agent steps
	callbackHandler := &agentLogHandler{console: hna.console}

	// // Connect to MCP server via stdio
	// mcpClient, err := client.NewStdioMCPClient("/home/vivazqu/workspace/azure-dev/cli/azd/tools/mcp/mcp", nil)
	// if err != nil {
	// 	log.Fatalf("Failed to create MCP client: %v", err)
	// }

	// defer mcpClient.Close()
	t := transport.NewStdioWithOptions("/home/vivazqu/workspace/azure-dev/cli/azd/tools/mcp/mcp", nil, nil)
	// Create sampling handler with LLM client
	samplingHandler := &samplingHandler{
		llmClient: llClient,
		console:   hna.console,
	}

	mcpClient := client.NewClient(t, client.WithSamplingHandler(samplingHandler))
	if err := mcpClient.Start(ctx); err != nil {
		return nil, fmt.Errorf("failed to start MCP client: %w", err)
	}
	defer mcpClient.Close()

	fmt.Println("🔌 MCP client created with sampling handler")

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

	fmt.Println("🤖 Starting AI agent execution...")
	fmt.Printf("   Agent has %d tools available from MCP server\n", len(tools))
	fmt.Println("   Sampling handler is configured for MCP tool requests")

	answer, err := chains.Run(ctx, executor, `
Say hello to Raul using the exact result from a tool to say hello to someone.
`,
		chains.WithTemperature(0.0),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to exe: %w", err)
	}
	fmt.Println("✅ AI agent execution completed")
	fmt.Println(answer)

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: "Done",
		},
	}, nil
}

type hookResolverTool struct {
}

func (h hookResolverTool) Name() string {
	return "Hook Resolver"
}

func (h hookResolverTool) Description() string {
	return `Useful for resolving the type of the hook based on the input.
	The input to this tool should be a string that contains the prompt that creates the hook.`
}

func (h hookResolverTool) Call(ctx context.Context, input string) (string, error) {
	validHookTypes := []string{"preprovision", "postprovision", "predeploy", "postdeploy"}
	for _, hookType := range validHookTypes {
		if strings.Contains(input, hookType) {
			return hookType, nil
		}
	}
	return "preprovision", nil
}

type osResolverTool struct {
}

func (h osResolverTool) Name() string {
	return "Os Resolver"
}

func (h osResolverTool) Description() string {
	return "Useful for resolving what is the user's operating system."
}

func (h osResolverTool) Call(ctx context.Context, input string) (string, error) {
	return runtime.GOOS, nil
}

type saveHookTool struct {
}

func (h saveHookTool) Name() string {
	return "Save Hook"
}

func (h saveHookTool) Description() string {
	return `Useful for saving the generated hook to a file.
    The input to this tool should be a JSON string with the following format:
	{
		"hookType": "<hook type>",
		"hookCode": "<hook code>"
	}.
	The input must be just the JSON string, without any additional text.`
}

func (h saveHookTool) Call(ctx context.Context, input string) (string, error) {
	// Parse the input JSON string
	var hookData struct {
		HookType string `json:"hookType"`
		HookCode string `json:"hookCode"`
	}
	if err := json.Unmarshal([]byte(input), &hookData); err != nil {
		return "", fmt.Errorf("failed to parse input JSON: %w", err)
	}

	// Save the hook code to a file
	if err := os.WriteFile(fmt.Sprintf("%s_hook.sh", hookData.HookType), []byte(hookData.HookCode), 0755); err != nil {
		return "", fmt.Errorf("failed to save hook file: %w", err)
	}

	return "Hook saved successfully", nil
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
		fmt.Print(string(chunk))
	}
}

func (h *agentLogHandler) HandleLLMStart(ctx context.Context, prompts []string) {
	h.step++
	fmt.Printf("🧠 Step %d: LLM processing...\n", h.step)
	if len(prompts) > 0 && len(prompts[0]) < 200 {
		fmt.Printf("   Prompt: %s\n", prompts[0])
	}
}

func (h *agentLogHandler) HandleLLMError(ctx context.Context, err error) {
	fmt.Printf("❌ Step %d: LLM error: %v\n", h.step, err)
}

func (h *agentLogHandler) HandleChainStart(ctx context.Context, inputs map[string]any) {
	fmt.Println("🚀 Agent chain started")
}

func (h *agentLogHandler) HandleChainEnd(ctx context.Context, outputs map[string]any) {
	fmt.Println("🏁 Agent chain completed")
}

func (h *agentLogHandler) HandleChainError(ctx context.Context, err error) {
	fmt.Printf("💥 Agent chain error: %v\n", err)
}

func (h *agentLogHandler) HandleToolStart(ctx context.Context, input string) {
	fmt.Printf("🔧 Using tool with input: %s\n", input)
	if input != "" && len(input) < 100 {
		fmt.Printf("   Input: %s\n", input)
	}
}

func (h *agentLogHandler) HandleToolEnd(ctx context.Context, output string) {
	if output != "" && len(output) < 150 {
		fmt.Printf("   Output: %s\n", output)
	} else {
		fmt.Println("   Tool completed")
	}
}

func (h *agentLogHandler) HandleToolError(ctx context.Context, err error) {
	fmt.Printf("   ❌ Tool error: %v\n", err)
}

func (h *agentLogHandler) HandleText(ctx context.Context, text string) {
	if text != "" && len(text) < 200 {
		fmt.Printf("💭 Agent thinking: %s\n", text)
	}
}

func (h *agentLogHandler) HandleAgentAction(ctx context.Context, action schema.AgentAction) {
	fmt.Printf("🎯 Agent action: %s\n", action.Tool)
	if action.ToolInput != "" && len(action.ToolInput) < 100 {
		fmt.Printf("   Tool input: %s\n", action.ToolInput)
	}
}

func (h *agentLogHandler) HandleAgentFinish(ctx context.Context, finish schema.AgentFinish) {
	fmt.Println("🏆 Agent finished successfully")
	if finish.ReturnValues != nil {
		fmt.Printf("   Final output: %v\n", finish.ReturnValues)
	}
}

func (h *agentLogHandler) HandleLLMGenerateContentEnd(ctx context.Context, response *llms.ContentResponse) {
	fmt.Println("✨ LLM content generation completed")
}
