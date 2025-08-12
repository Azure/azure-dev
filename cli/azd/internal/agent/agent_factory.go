package agent

import (
	"github.com/azure/azure-dev/cli/azd/internal/agent/consent"
	"github.com/azure/azure-dev/cli/azd/internal/agent/logging"
	localtools "github.com/azure/azure-dev/cli/azd/internal/agent/tools"
	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
	mcptools "github.com/azure/azure-dev/cli/azd/internal/agent/tools/mcp"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/llm"
)

type AgentFactory struct {
	consentManager consent.ConsentManager
	llmManager     *llm.Manager
	console        input.Console
}

func NewAgentFactory(
	consentManager consent.ConsentManager,
	console input.Console,
	llmManager *llm.Manager,
) *AgentFactory {
	return &AgentFactory{
		consentManager: consentManager,
		llmManager:     llmManager,
		console:        console,
	}
}

func (f *AgentFactory) Create(opts ...AgentOption) (Agent, func() error, error) {
	fileLogger, cleanup, err := logging.NewFileLoggerDefault()
	if err != nil {
		return nil, cleanup, err
	}

	defaultModelContainer, err := f.llmManager.GetDefaultModel(llm.WithLogger(fileLogger))
	if err != nil {
		return nil, cleanup, err
	}

	samplingModelContainer, err := f.llmManager.GetDefaultModel(llm.WithLogger(fileLogger))
	if err != nil {
		return nil, cleanup, err
	}

	// Create sampling handler for MCP
	samplingHandler := mcptools.NewMcpSamplingHandler(
		f.consentManager,
		f.console,
		samplingModelContainer,
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

	allTools := []common.AnnotatedTool{}

	for _, toolLoader := range toolLoaders {
		categoryTools, err := toolLoader.LoadTools()
		if err != nil {
			return nil, cleanup, err
		}

		// Filter out excluded tools
		for _, tool := range categoryTools {
			if !excludedTools[tool.Name()] {
				allTools = append(allTools, tool)
			}
		}
	}

	protectedTools := f.consentManager.WrapTools(allTools)

	allOptions := []AgentOption{}
	allOptions = append(allOptions, opts...)
	allOptions = append(allOptions, WithTools(protectedTools...))

	azdAgent, err := NewConversationalAzdAiAgent(defaultModelContainer.Model, allOptions...)
	if err != nil {
		return nil, cleanup, err
	}

	return azdAgent, cleanup, nil
}
