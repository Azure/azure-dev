// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package llm

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
)

type AzureOpenAiModelConfig struct {
	Model       string   `json:"model"`
	Version     string   `json:"version"`
	Endpoint    string   `json:"endpoint"`
	Token       string   `json:"token"`
	ApiVersion  string   `json:"apiVersion"`
	Temperature *float64 `json:"temperature"`
	MaxTokens   *int     `json:"maxTokens"`
}

type AzureOpenAiModelProvider struct {
	userConfigManager config.UserConfigManager
}

func NewAzureOpenAiModelProvider(userConfigManager config.UserConfigManager) ModelProvider {
	return &AzureOpenAiModelProvider{
		userConfigManager: userConfigManager,
	}
}

func (p *AzureOpenAiModelProvider) CreateModelContainer(opts ...ModelOption) (*ModelContainer, error) {
	userConfig, err := p.userConfigManager.Load()
	if err != nil {
		return nil, err
	}

	var modelConfig AzureOpenAiModelConfig
	if ok, err := userConfig.GetSection("ai.agent.model.azure", &modelConfig); !ok || err != nil {
		return nil, err
	}

	modelContainer := &ModelContainer{
		Type:    LlmTypeOpenAIAzure,
		IsLocal: false,
		Metadata: ModelMetadata{
			Name:    modelConfig.Model,
			Version: modelConfig.Version,
		},
		Url: modelConfig.Endpoint,
	}

	for _, opt := range opts {
		opt(modelContainer)
	}

	openAiModel, err := openai.New(
		openai.WithToken(modelConfig.Token),
		openai.WithBaseURL(modelConfig.Endpoint),
		openai.WithAPIType(openai.APITypeAzure),
		openai.WithAPIVersion(modelConfig.ApiVersion),
		openai.WithModel(modelConfig.Model),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM: %w", err)
	}

	callOptions := []llms.CallOption{}
	if modelConfig.Temperature != nil {
		callOptions = append(callOptions, llms.WithTemperature(*modelConfig.Temperature))
	}

	if modelConfig.MaxTokens != nil {
		callOptions = append(callOptions, llms.WithMaxTokens(*modelConfig.MaxTokens))
	}

	openAiModel.CallbacksHandler = modelContainer.logger
	modelContainer.Model = NewModel(openAiModel, callOptions...)

	return modelContainer, nil
}
