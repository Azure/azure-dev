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
	"github.com/tmc/langchaingo/tools"

	"github.com/azure/azure-dev/cli/azd/internal/agent/logging"
	localtools "github.com/azure/azure-dev/cli/azd/internal/agent/tools"
	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/mcp"
	mcptools "github.com/azure/azure-dev/cli/azd/internal/agent/tools/mcp"
)

//go:embed prompts/default_agent_prefix.txt
var _defaultAgentPrefix string

//go:embed prompts/default_agent_format_instructions.txt
var _defaultAgentFormatInstructions string

//go:embed prompts/default_agent_suffix.txt
var _defaultAgentSuffix string

// AzdAiAgent represents an enhanced AZD Copilot agent with action tracking, intent validation, and conversation memory
type AzdAiAgent struct {
	debug         bool
	defaultModel  llms.Model
	samplingModel llms.Model
	executor      *agents.Executor
}

type AgentOption func(*AzdAiAgent)

func WithDebug(debug bool) AgentOption {
	return func(agent *AzdAiAgent) {
		agent.debug = debug
	}
}

func WithSamplingModel(model llms.Model) AgentOption {
	return func(agent *AzdAiAgent) {
		agent.samplingModel = model
	}
}

func NewAzdAiAgent(llm llms.Model, opts ...AgentOption) (*AzdAiAgent, error) {
	azdAgent := &AzdAiAgent{
		defaultModel:  llm,
		samplingModel: llm,
	}

	for _, opt := range opts {
		opt(azdAgent)
	}

	actionLogger := logging.NewActionLogger(
		logging.WithDebug(azdAgent.debug),
	)

	smartMemory := memory.NewConversationBuffer(
		memory.WithInputKey("input"),
		memory.WithOutputKey("output"),
		memory.WithHumanPrefix("Human"),
		memory.WithAIPrefix("AI"),
	)

	// Create sampling handler for MCP
	samplingHandler := mcptools.NewMcpSamplingHandler(
		azdAgent.samplingModel,
		mcp.WithDebug(azdAgent.debug),
	)

	toolLoaders := []localtools.ToolLoader{
		localtools.NewLocalToolsLoader(actionLogger),
		mcptools.NewMcpToolsLoader(actionLogger, samplingHandler),
	}

	allTools := []tools.Tool{}

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
				allTools = append(allTools, tool)
			}
		}
	}

	// 4. Create agent with memory directly integrated
	conversationAgent := agents.NewConversationalAgent(llm, allTools,
		agents.WithPromptPrefix(_defaultAgentPrefix),
		agents.WithPromptSuffix(_defaultAgentSuffix),
		agents.WithPromptFormatInstructions(_defaultAgentFormatInstructions),
		agents.WithMemory(smartMemory),
		agents.WithCallbacksHandler(actionLogger),
		agents.WithReturnIntermediateSteps(),
	)

	// 5. Create executor without separate memory configuration since agent already has it
	executor := agents.NewExecutor(conversationAgent,
		agents.WithMaxIterations(500), // Much higher limit for complex multi-step processes
		agents.WithMemory(smartMemory),
		agents.WithCallbacksHandler(actionLogger),
		agents.WithReturnIntermediateSteps(),
	)

	azdAgent.executor = executor
	return azdAgent, nil
}

// RunConversationLoop runs the enhanced AZD Copilot agent with full capabilities
func (aai *AzdAiAgent) RunConversationLoop(ctx context.Context, args []string) error {
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
		err := aai.runChain(ctx, userInput)
		if err != nil {
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading input: %w", err)
	}

	return nil
}

// ProcessQuery processes a user query with full action tracking and validation
func (aai *AzdAiAgent) runChain(ctx context.Context, userInput string) error {
	// Execute with enhanced input - agent should automatically handle memory
	_, err := chains.Run(ctx, aai.executor, userInput,
		chains.WithMaxTokens(800),
		chains.WithTemperature(0.3),
	)
	if err != nil {
		return err
	}

	return nil
}
