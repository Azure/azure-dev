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

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
	"github.com/fatih/color"
	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/chains"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/memory"
	"github.com/tmc/langchaingo/prompts"
)

//go:embed prompts/conversational.txt
var conversational_prompt_template string

// ConversationalAzdAiAgent represents an enhanced `azd` agent with conversation memory,
// tool filtering, and interactive capabilities
type ConversationalAzdAiAgent struct {
	*agentBase
}

// NewConversationalAzdAiAgent creates a new conversational agent with memory, tool loading,
// and MCP sampling capabilities. It filters out excluded tools and configures the agent
// for interactive conversations with a high iteration limit for complex tasks.
func NewConversationalAzdAiAgent(llm llms.Model, opts ...AgentOption) (*ConversationalAzdAiAgent, error) {
	azdAgent := &ConversationalAzdAiAgent{
		agentBase: &agentBase{
			defaultModel: llm,
			tools:        []common.AnnotatedTool{},
		},
	}

	for _, opt := range opts {
		opt(azdAgent.agentBase)
	}

	smartMemory := memory.NewConversationBuffer(
		memory.WithInputKey("input"),
		memory.WithOutputKey("output"),
		memory.WithHumanPrefix("Human"),
		memory.WithAIPrefix("AI"),
	)

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
	conversationAgent := agents.NewConversationalAgent(llm, common.ToLangChainTools(azdAgent.tools),
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

// SendMessage processes a single message through the agent and returns the response
func (aai *ConversationalAzdAiAgent) SendMessage(ctx context.Context, args ...string) (string, error) {
	return aai.runChain(ctx, strings.Join(args, "\n"))
}

// StartConversation runs an interactive conversation loop with the agent.
// It accepts an optional initial query and handles user input/output with proper formatting.
// The conversation continues until the user types "exit" or "quit".
func (aai *ConversationalAzdAiAgent) StartConversation(ctx context.Context, args ...string) (string, error) {
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
			fmt.Println("👋 Goodbye! Thanks for using azd Agent!")
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

// runChain executes a user query through the agent's chain with memory and returns the response
func (aai *ConversationalAzdAiAgent) runChain(ctx context.Context, userInput string) (string, error) {
	// Execute with enhanced input - agent should automatically handle memory
	output, err := chains.Run(ctx, aai.executor, userInput)
	if err != nil {
		return "", err
	}
	return output, nil
}
