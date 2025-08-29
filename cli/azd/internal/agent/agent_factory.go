// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"github.com/azure/azure-dev/cli/azd/internal/agent/consent"
	"github.com/azure/azure-dev/cli/azd/internal/agent/logging"
	"github.com/azure/azure-dev/cli/azd/internal/agent/security"
	localtools "github.com/azure/azure-dev/cli/azd/internal/agent/tools"
	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
	mcptools "github.com/azure/azure-dev/cli/azd/internal/agent/tools/mcp"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/llm"
)

// AgentFactory is responsible for creating agent instances
type AgentFactory struct {
	consentManager  consent.ConsentManager
	llmManager      *llm.Manager
	console         input.Console
	securityManager *security.Manager
}

// NewAgentFactory creates a new instance of AgentFactory
func NewAgentFactory(
	consentManager consent.ConsentManager,
	console input.Console,
	llmManager *llm.Manager,
	securityManager *security.Manager,
) *AgentFactory {
	return &AgentFactory{
		consentManager:  consentManager,
		llmManager:      llmManager,
		console:         console,
		securityManager: securityManager,
	}
}

// CreateAgent creates a new agent instance
func (f *AgentFactory) Create(opts ...AgentCreateOption) (Agent, error) {
	// Create a daily log file for all agent activity
	fileLogger, loggerCleanup, err := logging.NewFileLoggerDefault()
	if err != nil {
		defer loggerCleanup()
		return nil, err
	}

	// Create a channel for logging thoughts & actions
	thoughtChan := make(chan logging.Thought)
	thoughtHandler := logging.NewThoughtLogger(thoughtChan)
	chainedHandler := logging.NewChainedHandler(fileLogger, thoughtHandler)

	cleanup := func() error {
		close(thoughtChan)
		return loggerCleanup()
	}

	// Default model gets the chained handler to expose the UX experience for the agent
	defaultModelContainer, err := f.llmManager.GetDefaultModel(llm.WithLogger(chainedHandler))
	if err != nil {
		defer cleanup()
		return nil, err
	}

	// Sampling model only gets the file logger to output sampling actions
	// We don't need UX for sampling requests right now
	samplingModelContainer, err := f.llmManager.GetDefaultModel(llm.WithLogger(fileLogger))
	if err != nil {
		defer cleanup()
		return nil, err
	}

	// Create sampling handler for MCP
	samplingHandler := mcptools.NewMcpSamplingHandler(
		f.consentManager,
		f.console,
		samplingModelContainer,
	)

	// Loads build-in tools & any referenced MCP servers
	toolLoaders := []common.ToolLoader{
		localtools.NewLocalToolsLoader(f.securityManager),
		mcptools.NewMcpToolsLoader(samplingHandler),
	}

	// Define block list of excluded tools
	excludedTools := map[string]bool{
		"extension_az":  true,
		"extension_azd": true,
		// Add more excluded tools here as needed
	}

	allTools := []common.AnnotatedTool{}

	for _, toolLoader := range toolLoaders {
		categoryTools, err := toolLoader.LoadTools()
		if err != nil {
			defer cleanup()
			return nil, err
		}

		// Filter out excluded tools
		for _, tool := range categoryTools {
			if !excludedTools[tool.Name()] {
				allTools = append(allTools, tool)
			}
		}
	}

	// Wraps all tools in consent workflow
	protectedTools := f.consentManager.WrapTools(allTools)

	// Finalize agent creation options
	allOptions := []AgentCreateOption{}
	allOptions = append(allOptions, opts...)
	allOptions = append(allOptions,
		WithCallbacksHandler(chainedHandler),
		WithThoughtChannel(thoughtChan),
		WithTools(protectedTools...),
		WithCleanup(cleanup),
	)

	azdAgent, err := NewConversationalAzdAiAgent(defaultModelContainer.Model, allOptions...)
	if err != nil {
		return nil, err
	}

	return azdAgent, nil
}
