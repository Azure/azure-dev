// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package llm

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
)

// CopilotProvider implements ModelProvider for the Copilot SDK agent type.
// Unlike Azure OpenAI or Ollama, the Copilot SDK handles the full agent runtime —
// this provider returns a ModelContainer marker that signals the agent factory
// to use CopilotAgentFactory instead of the langchaingo-based AgentFactory.
type CopilotProvider struct {
	userConfigManager config.UserConfigManager
}

// NewCopilotProvider creates a new Copilot provider.
func NewCopilotProvider(userConfigManager config.UserConfigManager) ModelProvider {
	return &CopilotProvider{
		userConfigManager: userConfigManager,
	}
}

// CreateModelContainer returns a ModelContainer for the Copilot SDK.
// The Model field is nil because the Copilot SDK manages the full agent runtime
// via copilot.Session — the container serves as a type marker for agent factory selection.
func (p *CopilotProvider) CreateModelContainer(
	ctx context.Context, opts ...ModelOption,
) (*ModelContainer, error) {
	container := &ModelContainer{
		Type:    LlmTypeCopilot,
		IsLocal: false,
		Metadata: ModelMetadata{
			Name:    "copilot",
			Version: "latest",
		},
	}

	// Read optional model name from config
	userConfig, err := p.userConfigManager.Load()
	if err == nil {
		if model, ok := userConfig.GetString("ai.agent.model"); ok {
			container.Metadata.Name = model
		}
	}

	for _, opt := range opts {
		opt(container)
	}

	return container, nil
}
