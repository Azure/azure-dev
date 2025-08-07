// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package llm

import (
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/ollama"
)

type OllamaModelConfig struct {
	Model       string   `json:"model"`
	Version     string   `json:"version"`
	Temperature *float64 `json:"temperature"`
	MaxTokens   *int     `json:"maxTokens"`
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

	defaultModel := "llama3"

	var modelConfig OllamaModelConfig
	ok, err := userConfig.GetSection("ai.agent.model.ollama", &modelConfig)
	if err != nil {
		return nil, err
	}

	if ok {
		defaultModel = modelConfig.Model
	}

	// Set defaults if not defined
	if modelConfig.Version == "" {
		modelConfig.Version = "latest"
	}

	modelContainer := &ModelContainer{
		Type:    LlmTypeOllama,
		IsLocal: true,
		Metadata: ModelMetadata{
			Name:    defaultModel,
			Version: modelConfig.Version,
		},
	}

	for _, opt := range opts {
		opt(modelContainer)
	}

	ollamaModel, err := ollama.New(
		ollama.WithModel(defaultModel),
	)
	if err != nil {
		return nil, err
	}

	callOptions := []llms.CallOption{}
	if modelConfig.Temperature != nil {
		callOptions = append(callOptions, llms.WithTemperature(*modelConfig.Temperature))
	}

	if modelConfig.MaxTokens != nil {
		callOptions = append(callOptions, llms.WithMaxTokens(*modelConfig.MaxTokens))
	}

	ollamaModel.CallbacksHandler = modelContainer.logger
	modelContainer.Model = newModelWithCallOptions(ollamaModel, callOptions...)

	return modelContainer, nil
}
