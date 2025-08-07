// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"bufio"
	"context"
	_ "embed"
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/chains"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/memory"
	"github.com/tmc/langchaingo/prompts"
	"github.com/tmc/langchaingo/tools"

	localtools "github.com/azure/azure-dev/cli/azd/internal/agent/tools"
	mcptools "github.com/azure/azure-dev/cli/azd/internal/agent/tools/mcp"
)

//go:embed prompts/conversational.txt
var conversational_prompt_template string

// ConversationalAzdAiAgent represents an enhanced AZD Copilot agent with action tracking,
// intent validation, and conversation memory
type ConversationalAzdAiAgent struct {
	*Agent
}

func NewConversationalAzdAiAgent(llm llms.Model, opts ...AgentOption) (*ConversationalAzdAiAgent, error) {
	azdAgent := &ConversationalAzdAiAgent{
		Agent: &Agent{
			defaultModel:  llm,
			samplingModel: llm,
			tools:         []tools.Tool{},
		},
	}

	for _, opt := range opts {
		opt(azdAgent.Agent)
	}

	smartMemory := memory.NewConversationBuffer(
		memory.WithInputKey("input"),
		memory.WithOutputKey("output"),
		memory.WithHumanPrefix("Human"),
		memory.WithAIPrefix("AI"),
	)

	// Create sampling handler for MCP
	samplingHandler := mcptools.NewMcpSamplingHandler(
		azdAgent.samplingModel,
		mcptools.WithDebug(azdAgent.debug),
	)

	toolLoaders := []localtools.ToolLoader{
		localtools.NewLocalToolsLoader(),
		mcptools.NewMcpToolsLoader(samplingHandler),
	}

	// Define block list of excluded tools
	excludedTools := map[string]bool{
		"extension_az":  true,
		"extension_azd": true,
		// Add more excluded tools here as needed
	}

	for _, toolLoader := range toolLoaders {
		categoryTools, err := toolLoader.LoadTools()
		if err != nil {
			return nil, err
		}

		// Filter out excluded tools
		for _, tool := range categoryTools {
			if !excludedTools[tool.Name()] {
				azdAgent.tools = append(azdAgent.tools, tool)
			}
		}
	}

	promptTemplate := prompts.PromptTemplate{
		Template:       conversational_prompt_template,
		TemplateFormat: prompts.TemplateFormatGoTemplate,
		InputVariables: []string{"input", "agent_scratchpad"},
		PartialVariables: map[string]any{
			"tool_names":        toolNames(azdAgent.tools),
			"tool_descriptions": toolDescriptions(azdAgent.tools),
			"history":           "",
		},
	}

	// 4. Create agent with memory directly integrated
	conversationAgent := agents.NewConversationalAgent(llm, azdAgent.tools,
		agents.WithPrompt(promptTemplate),
		agents.WithMemory(smartMemory),
		agents.WithCallbacksHandler(azdAgent.callbacksHandler),
		agents.WithReturnIntermediateSteps(),
	)

	// 5. Create executor without separate memory configuration since agent already has it
	executor := agents.NewExecutor(conversationAgent,
		agents.WithMaxIterations(500), // Much higher limit for complex multi-step processes
		agents.WithMemory(smartMemory),
		agents.WithCallbacksHandler(azdAgent.callbacksHandler),
		agents.WithReturnIntermediateSteps(),
	)

	azdAgent.executor = executor
	return azdAgent, nil
}

func (aai *ConversationalAzdAiAgent) SendMessage(ctx context.Context, args ...string) (string, error) {
	return aai.runChain(ctx, strings.Join(args, "\n"))
}

// RunConversationLoop runs the enhanced AZD Copilot agent with full capabilities
func (aai *ConversationalAzdAiAgent) StartConversation(ctx context.Context, args ...string) (string, error) {
	fmt.Println("🤖 AZD Copilot - Interactive Mode")
	fmt.Println("═══════════════════════════════════════════════════════════")

	// Handle initial query if provided
	var initialQuery string
	if len(args) > 0 {
		initialQuery = strings.Join(args, " ")
	}

	scanner := bufio.NewScanner(os.Stdin)

	for {
		var userInput string

		if initialQuery != "" {
			userInput = initialQuery
			initialQuery = "" // Clear after first use
			color.Cyan("💬 You: %s\n", userInput)
		} else {
			fmt.Print(color.CyanString("\n💬 You: "))
			color.Set(color.FgCyan) // Set blue color for user input
			if !scanner.Scan() {
				color.Unset() // Reset color
				break         // EOF or error
			}
			userInput = strings.TrimSpace(scanner.Text())
			color.Unset() // Reset color after input
		}

		// Check for exit commands
		if userInput == "" {
			continue
		}

		if strings.ToLower(userInput) == "exit" || strings.ToLower(userInput) == "quit" {
			fmt.Println("👋 Goodbye! Thanks for using AZD Copilot!")
			break
		}

		// Process the query with the enhanced agent
		return aai.runChain(ctx, userInput)
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading input: %w", err)
	}

	return "", nil
}

// ProcessQuery processes a user query with full action tracking and validation
func (aai *ConversationalAzdAiAgent) runChain(ctx context.Context, userInput string) (string, error) {
	// Execute with enhanced input - agent should automatically handle memory
	output, err := chains.Run(ctx, aai.executor, userInput)
	if err != nil {
		return "", err
	}
	return output, nil
}
