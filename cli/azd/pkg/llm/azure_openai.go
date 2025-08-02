// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package llm

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/tmc/langchaingo/llms/openai"
)

type AzureOpenAiModelConfig struct {
	Model      string `json:"model"`
	Version    string `json:"version"`
	Endpoint   string `json:"endpoint"`
	Token      string `json:"token"`
	ApiVersion string `json:"apiVersion"`
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

	model, err := openai.New(
		openai.WithToken(modelConfig.Token),
		openai.WithBaseURL(modelConfig.Endpoint),
		openai.WithAPIType(openai.APITypeAzure),
		openai.WithAPIVersion(modelConfig.ApiVersion),
		openai.WithModel(modelConfig.Model),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM: %w", err)
	}

	model.CallbacksHandler = modelContainer.logger
	modelContainer.Model = model

	return modelContainer, nil
}
