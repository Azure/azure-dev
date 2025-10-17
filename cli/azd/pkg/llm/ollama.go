// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package llm

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/ollama"
)

// OllamaModelConfig holds configuration settings for Ollama models
type OllamaModelConfig struct {
	Model       string   `json:"model"`
	Version     string   `json:"version"`
	Temperature *float64 `json:"temperature"`
	MaxTokens   *int     `json:"maxTokens"`
}

// OllamaModelProvider creates Ollama models from user configuration with sensible defaults
type OllamaModelProvider struct {
	userConfigManager config.UserConfigManager
}

// NewOllamaModelProvider creates a new Ollama model provider
func NewOllamaModelProvider(userConfigManager config.UserConfigManager) ModelProvider {
	return &OllamaModelProvider{
		userConfigManager: userConfigManager,
	}
}

// CreateModelContainer creates a model container for Ollama with configuration from user settings.
// It defaults to "llama3" model if none specified and "latest" version if not configured.
// Applies optional parameters like temperature and max tokens to the Ollama client.
func (p *OllamaModelProvider) CreateModelContainer(_ context.Context, opts ...ModelOption) (*ModelContainer, error) {
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
