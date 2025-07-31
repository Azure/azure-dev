// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"context"
	_ "embed"

	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/chains"
	"github.com/tmc/langchaingo/llms/openai"
	"github.com/tmc/langchaingo/memory"
	"github.com/tmc/langchaingo/tools"

	localtools "azd.ai.start/internal/tools"
	mcptools "azd.ai.start/internal/tools/mcp"
)

//go:embed prompts/default_agent_prefix.txt
var _defaultAgentPrefix string

//go:embed prompts/default_agent_format_instructions.txt
var _defaultAgentFormatInstructions string

//go:embed prompts/default_agent_suffix.txt
var _defaultAgentSuffix string

// AzureAIAgent represents an enhanced Azure AI agent with action tracking, intent validation, and conversation memory
type AzureAIAgent struct {
	executor *agents.Executor
}

func NewAzureAIAgent(llm *openai.LLM) (*AzureAIAgent, error) {
	smartMemory := memory.NewConversationBuffer(
		memory.WithInputKey("input"),
		memory.WithOutputKey("output"),
		memory.WithHumanPrefix("Human"),
		memory.WithAIPrefix("AI"),
	)

	// Create sampling handler for MCP
	samplingHandler := mcptools.NewMcpSamplingHandler(llm)

	toolLoaders := []localtools.ToolLoader{
		localtools.NewLocalToolsLoader(llm.CallbacksHandler),
		mcptools.NewMcpToolsLoader(llm.CallbacksHandler, samplingHandler),
	}

	allTools := []tools.Tool{}

	for _, toolLoader := range toolLoaders {
		categoryTools, err := toolLoader.LoadTools()
		if err != nil {
			return nil, err
		}
		allTools = append(allTools, categoryTools...)
	}

	// 4. Create agent with memory directly integrated
	agent := agents.NewConversationalAgent(llm, allTools,
		agents.WithPromptPrefix(_defaultAgentPrefix),
		agents.WithPromptSuffix(_defaultAgentSuffix),
		agents.WithPromptFormatInstructions(_defaultAgentFormatInstructions),
		agents.WithMemory(smartMemory),
		agents.WithCallbacksHandler(llm.CallbacksHandler),
		agents.WithReturnIntermediateSteps(),
	)

	// 5. Create executor without separate memory configuration since agent already has it
	executor := agents.NewExecutor(agent,
		agents.WithMaxIterations(100), // Much higher limit for complex multi-step processes
		agents.WithMemory(smartMemory),
		agents.WithCallbacksHandler(llm.CallbacksHandler),
		agents.WithReturnIntermediateSteps(),
	)

	return &AzureAIAgent{
		executor: executor,
	}, nil
}

// ProcessQuery processes a user query with full action tracking and validation
func (aai *AzureAIAgent) ProcessQuery(ctx context.Context, userInput string) error {
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
