// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"context"
	_ "embed"

	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/chains"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/memory"
	"github.com/tmc/langchaingo/tools"

	"azd.ai.start/internal/agent/logging"
	localtools "azd.ai.start/internal/agent/tools"
	"azd.ai.start/internal/agent/tools/mcp"
	mcptools "azd.ai.start/internal/agent/tools/mcp"
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

// ProcessQuery processes a user query with full action tracking and validation
func (aai *AzdAiAgent) ProcessQuery(ctx context.Context, userInput string) error {
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
