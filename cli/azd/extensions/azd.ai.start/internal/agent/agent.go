// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/chains"
	"github.com/tmc/langchaingo/llms/openai"
	"github.com/tmc/langchaingo/memory"
	"github.com/tmc/langchaingo/schema"
	"github.com/tmc/langchaingo/tools"

	"azd.ai.start/internal/session"
	mytools "azd.ai.start/internal/tools"
)

//go:embed prompts/default_agent_prefix.txt
var _defaultAgentPrefix string

// AzureAIAgent represents an enhanced Azure AI agent with action tracking, intent validation, and conversation memory
type AzureAIAgent struct {
	agent          *agents.ConversationalAgent
	executor       *agents.Executor
	memory         schema.Memory // Maintains conversation history for context-aware responses
	tools          []tools.Tool
	actionLogger   callbacks.Handler
	currentSession *session.ActionSession
}

func NewAzureAIAgent(llm *openai.LLM) *AzureAIAgent {
	smartMemory := memory.NewConversationBuffer(
		memory.WithInputKey("input"),
		memory.WithOutputKey("output"),
		memory.WithHumanPrefix("Human"),
		memory.WithAIPrefix("AI"),
	)

	tools := []tools.Tool{
		// Directory operations
		mytools.DirectoryListTool{
			CallbacksHandler: llm.CallbacksHandler,
		},
		mytools.CreateDirectoryTool{
			CallbacksHandler: llm.CallbacksHandler,
		},
		mytools.DeleteDirectoryTool{
			CallbacksHandler: llm.CallbacksHandler,
		},
		mytools.ChangeDirectoryTool{
			CallbacksHandler: llm.CallbacksHandler,
		},
		mytools.CurrentDirectoryTool{
			CallbacksHandler: llm.CallbacksHandler,
		},

		// File operations
		mytools.ReadFileTool{
			CallbacksHandler: llm.CallbacksHandler,
		},
		mytools.WriteFileTool{
			CallbacksHandler: llm.CallbacksHandler,
		},
		mytools.CopyFileTool{
			CallbacksHandler: llm.CallbacksHandler,
		},
		mytools.MoveFileTool{
			CallbacksHandler: llm.CallbacksHandler,
		},
		mytools.DeleteFileTool{
			CallbacksHandler: llm.CallbacksHandler,
		},
		mytools.FileInfoTool{
			CallbacksHandler: llm.CallbacksHandler,
		},

		// Other tools
		mytools.HTTPFetcherTool{
			CallbacksHandler: llm.CallbacksHandler,
		},
		mytools.WeatherTool{
			CallbacksHandler: llm.CallbacksHandler,
		},
		tools.Calculator{
			CallbacksHandler: llm.CallbacksHandler,
		},
	}

	// 4. Create agent with memory directly integrated
	agent := agents.NewConversationalAgent(llm, tools,
		agents.WithPromptPrefix(_defaultAgentPrefix),
		agents.WithMemory(smartMemory),
		agents.WithCallbacksHandler(llm.CallbacksHandler),
	)

	// 5. Create executor without separate memory configuration since agent already has it
	executor := agents.NewExecutor(agent,
		agents.WithMaxIterations(1000), // Much higher limit for complex multi-step processes
		agents.WithMemory(smartMemory),
		agents.WithCallbacksHandler(llm.CallbacksHandler),
		agents.WithReturnIntermediateSteps(),
	)

	return &AzureAIAgent{
		agent:        agent,
		executor:     executor,
		memory:       smartMemory,
		tools:        tools,
		actionLogger: llm.CallbacksHandler,
	}
}

// ProcessQuery processes a user query with full action tracking and validation
func (aai *AzureAIAgent) ProcessQuery(ctx context.Context, userInput string) (string, error) {
	// Execute with enhanced input - agent should automatically handle memory
	output, err := chains.Run(ctx, aai.executor, userInput)
	if err != nil {
		fmt.Printf("❌ Execution failed: %s\n", err.Error())
		return "", err
	}

	return output, nil
}
