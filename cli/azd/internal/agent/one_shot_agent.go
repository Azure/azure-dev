// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"context"
	_ "embed"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/chains"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/prompts"
)

// OneShotAzdAiAgent represents an `azd` agent designed for single-request processing
// without conversation memory, optimized for one-time queries and responses
type OneShotAzdAiAgent struct {
	*agentBase
}

//go:embed prompts/one_shot.txt
var one_shot_prompt_template string

// NewOneShotAzdAiAgent creates a new one-shot agent optimized for single queries.
// It loads tools from multiple sources, filters excluded tools, and configures
// the agent for stateless operation without conversation memory.
func NewOneShotAzdAiAgent(llm llms.Model, opts ...AgentOption) (*OneShotAzdAiAgent, error) {
	azdAgent := &OneShotAzdAiAgent{
		agentBase: &agentBase{
			defaultModel: llm,
			tools:        []common.AnnotatedTool{},
		},
	}

	for _, opt := range opts {
		opt(azdAgent.agentBase)
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
	oneShotAgent := agents.NewOneShotAgent(llm, common.ToLangChainTools(azdAgent.tools),
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
