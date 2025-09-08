// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package llm

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/llms"
)

// FeatureLlm is the feature key for the LLM (Large Language Model) feature.
var FeatureLlm = alpha.MustFeatureKey("llm")

// IsLlmFeatureEnabled checks if the LLM feature is enabled.
func IsLlmFeatureEnabled(alphaManager *alpha.FeatureManager) error {
	if alphaManager == nil {
		panic("alphaManager cannot be nil")
	}
	if !alphaManager.IsEnabled(FeatureLlm) {
		return fmt.Errorf("the LLM feature is not enabled. Please enable it using the command: \"%s\"",
			alpha.GetEnableCommand(FeatureLlm))
	}
	return nil
}

// NewManager creates a new instance of the LLM Manager.
func NewManager(
	alphaManager *alpha.FeatureManager,
	userConfigManager config.UserConfigManager,
	modelFactory *ModelFactory,
) *Manager {
	return &Manager{
		alphaManager:      alphaManager,
		userConfigManager: userConfigManager,
		ModelFactory:      modelFactory,
	}
}

// Manager provides functionality to manage Language Model (LLM) features and capabilities.
// It encapsulates the alpha feature manager to control access to experimental LLM features.
type Manager struct {
	alphaManager      *alpha.FeatureManager
	userConfigManager config.UserConfigManager
	ModelFactory      *ModelFactory
}

// LlmType represents the type of language model.
type LlmType string

// String returns the string representation of the LlmType.
func (l LlmType) String() string {
	switch l {
	case LlmTypeOllama:
		return "Ollama"
	case LlmTypeOpenAIAzure:
		return "OpenAI Azure"
	case LlmTypeGhCp:
		return "GitHub Copilot"
	default:
		return string(l)
	}
}

const (
	// LlmTypeOpenAIAzure represents the Azure OpenAI model type.
	LlmTypeOpenAIAzure LlmType = "azure"
	// LlmTypeOllama represents the Ollama model type.
	LlmTypeOllama LlmType = "ollama"
	// LlmTypeGhCp represents the GitHub Copilot model type.
	LlmTypeGhCp LlmType = "github-copilot"
)

// ModelMetadata represents a language model with its name and version information.
// Name specifies the identifier of the language model.
// Version indicates the specific version or release of the model.
type ModelMetadata struct {
	Name    string
	Version string
}

// ModelContainer represents the configuration information of a Language Learning Model (LLM).
// It contains details about the model type, deployment location, model specification,
// and endpoint URL for remote models.
type ModelContainer struct {
	Type     LlmType
	IsLocal  bool
	Metadata ModelMetadata
	Model    llms.Model
	Url      string // For remote models, this is the API endpoint URL
	logger   callbacks.Handler
}

// ModelOption is a functional option for configuring a ModelContainer
type ModelOption func(modelContainer *ModelContainer)

// WithLogger returns an option that sets the logger for the model container
func WithLogger(logger callbacks.Handler) ModelOption {
	return func(modelContainer *ModelContainer) {
		modelContainer.logger = logger
	}
}

// NotEnabledError represents an error that occurs when LLM functionality is not enabled.
// This error is typically raised when attempting to use LLM features that have not been
// activated or configured in the system.
type NotEnabledError struct {
}

func (e NotEnabledError) Error() string {
	return fmt.Sprintf("LLM feature is not enabled. Run '%s' to enable",
		alpha.GetEnableCommand(FeatureLlm))
}

// InvalidLlmConfiguration represents an error that occurs when the LLM (Large Language Model)
// configuration is invalid or improperly formatted. This error type is used to indicate
// configuration-related issues in the LLM system.
type InvalidLlmConfiguration struct {
}

func (e InvalidLlmConfiguration) Error() string {
	return "Unable to determine LLM configuration. Please check your environment variables or configuration."
}

// GetDefaultModel returns the configured model from the global azd user configuration
func (m Manager) GetDefaultModel(ctx context.Context, opts ...ModelOption) (*ModelContainer, error) {
	userConfig, err := m.userConfigManager.Load()
	if err != nil {
		return nil, err
	}

	defaultModelType, ok := userConfig.GetString("ai.agent.model.type")
	if !ok {
		return nil, fmt.Errorf("Default model type has not been set")
	}

	return m.ModelFactory.CreateModelContainer(ctx, LlmType(defaultModelType), opts...)
}
