// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package llm

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
)

// AzureOpenAiModelConfig holds configuration settings for Azure OpenAI models
type AzureOpenAiModelConfig struct {
	Model       string   `json:"model"`
	Version     string   `json:"version"`
	Endpoint    string   `json:"endpoint"`
	Token       string   `json:"token"`
	ApiVersion  string   `json:"apiVersion"`
	Temperature *float64 `json:"temperature"`
	MaxTokens   *int     `json:"maxTokens"`
}

// AzureOpenAiModelProvider creates Azure OpenAI models from user configuration
type AzureOpenAiModelProvider struct {
	userConfigManager config.UserConfigManager
}

// NewAzureOpenAiModelProvider creates a new Azure OpenAI model provider
func NewAzureOpenAiModelProvider(userConfigManager config.UserConfigManager) ModelProvider {
	return &AzureOpenAiModelProvider{
		userConfigManager: userConfigManager,
	}
}

// CreateModelContainer creates a model container for Azure OpenAI with configuration
// loaded from user settings. It validates required fields and applies optional parameters
// like temperature and max tokens before creating the OpenAI client.
func (p *AzureOpenAiModelProvider) CreateModelContainer(_ context.Context, opts ...ModelOption) (*ModelContainer, error) {
	userConfig, err := p.userConfigManager.Load()
	if err != nil {
		return nil, err
	}

	var modelConfig AzureOpenAiModelConfig
	if ok, err := userConfig.GetSection("ai.agent.model.azure", &modelConfig); !ok || err != nil {
		return nil, err
	}

	// Validate required attributes
	requiredFields := map[string]string{
		"token":      modelConfig.Token,
		"endpoint":   modelConfig.Endpoint,
		"apiVersion": modelConfig.ApiVersion,
		"model":      modelConfig.Model,
	}

	for fieldName, fieldValue := range requiredFields {
		if fieldValue == "" {
			return nil, fmt.Errorf("azure openai model configuration is missing required '%s' field", fieldName)
		}
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
	modelContainer.Model = newModelWithCallOptions(openAiModel, callOptions...)

	return modelContainer, nil
}
