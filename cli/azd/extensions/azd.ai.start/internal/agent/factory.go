// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/llms/openai"
	"github.com/tmc/langchaingo/memory"
	"github.com/tmc/langchaingo/tools"

	"azd.ai.start/internal/logging"
	mytools "azd.ai.start/internal/tools"
	"azd.ai.start/internal/validation"
)

// CreateAzureAIAgent creates a new enhanced Azure AI agent
func CreateAzureAIAgent(llm *openai.LLM) *AzureAIAgent {
	// 1. Smart Memory with conversation buffer
	smartMemory := memory.NewConversationBuffer()

	// 2. Action Logger with comprehensive callbacks
	actionLogger := logging.NewActionLogger()

	// 3. Enhanced Tools - just the essentials
	tools := []tools.Tool{
		// Directory operations
		mytools.DirectoryListTool{},
		mytools.CreateDirectoryTool{},
		mytools.DeleteDirectoryTool{},
		mytools.ChangeDirectoryTool{},
		mytools.CurrentDirectoryTool{},

		// File operations
		mytools.ReadFileTool{},
		mytools.WriteFileTool{},
		mytools.CopyFileTool{},
		mytools.MoveFileTool{},
		mytools.DeleteFileTool{},
		mytools.FileInfoTool{},

		// Other tools
		mytools.HTTPFetcherTool{},
		mytools.WeatherTool{},
		tools.Calculator{},
	}

	// 4. Create agent with default settings
	agent := agents.NewConversationalAgent(llm, tools)

	// 5. Enhanced Executor with aggressive completion settings
	executor := agents.NewExecutor(agent,
		agents.WithMemory(smartMemory),
		agents.WithMaxIterations(1000), // Much higher limit for complex multi-step processes
		agents.WithReturnIntermediateSteps(),
	)

	return &AzureAIAgent{
		agent:           agent,
		executor:        executor,
		memory:          smartMemory,
		tools:           tools,
		intentValidator: validation.NewIntentValidator(llm),
		actionLogger:    actionLogger,
	}
}
