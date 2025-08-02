// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package llm

import (
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/tmc/langchaingo/llms/ollama"
)

type OllamaModelConfig struct {
	Model string `json:"model"`
}

type OllamaModelProvider struct {
	userConfigManager config.UserConfigManager
}

func NewOllamaModelProvider(userConfigManager config.UserConfigManager) ModelProvider {
	return &OllamaModelProvider{
		userConfigManager: userConfigManager,
	}
}

func (p *OllamaModelProvider) CreateModelContainer(opts ...ModelOption) (*ModelContainer, error) {
	userConfig, err := p.userConfigManager.Load()
	if err != nil {
		return nil, err
	}

	defaultLlamaVersion := "llama3"

	var modelConfig OllamaModelConfig
	ok, err := userConfig.GetSection("ai.agent.model.ollama", &modelConfig)
	if err != nil {
		return nil, err
	}

	if ok {
		defaultLlamaVersion = modelConfig.Model
	}

	modelContainer := &ModelContainer{
		Type:    LlmTypeOllama,
		IsLocal: true,
		Metadata: ModelMetadata{
			Name:    defaultLlamaVersion,
			Version: "latest",
		},
	}

	for _, opt := range opts {
		opt(modelContainer)
	}

	model, err := ollama.New(
		ollama.WithModel(defaultLlamaVersion),
	)
	if err != nil {
		return nil, err
	}

	model.CallbacksHandler = modelContainer.logger
	modelContainer.Model = model

	return modelContainer, nil
}
