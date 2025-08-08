// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"context"
	_ "embed"
	"strings"

	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/chains"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/prompts"
	"github.com/tmc/langchaingo/tools"

	localtools "github.com/azure/azure-dev/cli/azd/internal/agent/tools"
	mcptools "github.com/azure/azure-dev/cli/azd/internal/agent/tools/mcp"
)

// OneShotAzdAiAgent represents an AZD Copilot agent designed for single-request processing
// without conversation memory, optimized for one-time queries and responses
type OneShotAzdAiAgent struct {
	*Agent
}

//go:embed prompts/one_shot.txt
var one_shot_prompt_template string

// NewOneShotAzdAiAgent creates a new one-shot agent optimized for single queries.
// It loads tools from multiple sources, filters excluded tools, and configures
// the agent for stateless operation without conversation memory.
func NewOneShotAzdAiAgent(llm llms.Model, opts ...AgentOption) (*OneShotAzdAiAgent, error) {
	azdAgent := &OneShotAzdAiAgent{
		Agent: &Agent{
			defaultModel:  llm,
			samplingModel: llm,
			tools:         []tools.Tool{},
		},
	}

	for _, opt := range opts {
		opt(azdAgent.Agent)
	}

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
		Template:       one_shot_prompt_template,
		InputVariables: []string{"input", "agent_scratchpad"},
		TemplateFormat: prompts.TemplateFormatGoTemplate,
		PartialVariables: map[string]any{
			"tool_names":        toolNames(azdAgent.tools),
			"tool_descriptions": toolDescriptions(azdAgent.tools),
		},
	}

	// 4. Create agent with memory directly integrated
	oneShotAgent := agents.NewOneShotAgent(llm, azdAgent.tools,
		agents.WithPrompt(promptTemplate),
		agents.WithCallbacksHandler(azdAgent.callbacksHandler),
		agents.WithReturnIntermediateSteps(),
	)

	// 5. Create executor without separate memory configuration since agent already has it
	executor := agents.NewExecutor(oneShotAgent,
		agents.WithMaxIterations(500), // Much higher limit for complex multi-step processes
		agents.WithCallbacksHandler(azdAgent.callbacksHandler),
		agents.WithReturnIntermediateSteps(),
	)

	azdAgent.executor = executor
	return azdAgent, nil
}

// SendMessage processes a single message through the one-shot agent and returns the response
func (aai *OneShotAzdAiAgent) SendMessage(ctx context.Context, args ...string) (string, error) {
	return aai.runChain(ctx, strings.Join(args, "\n"))
}

// runChain executes a user query through the one-shot agent without memory persistence
func (aai *OneShotAzdAiAgent) runChain(ctx context.Context, userInput string) (string, error) {
	// Execute with enhanced input - agent should automatically handle memory
	output, err := chains.Run(ctx, aai.executor, userInput)
	if err != nil {
		return "", err
	}

	return output, nil
}
