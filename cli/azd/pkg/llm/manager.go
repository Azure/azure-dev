// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package llm

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/tmc/langchaingo/llms/ollama"
	"github.com/tmc/langchaingo/llms/openai"
)

var featureLlm = alpha.MustFeatureKey("llm")

func IsLlmFeatureEnabled(alphaManager *alpha.FeatureManager) error {
	if alphaManager == nil {
		panic("alphaManager cannot be nil")
	}
	if !alphaManager.IsEnabled(featureLlm) {
		return fmt.Errorf("the LLM feature is not enabled. Please enable it using the command: \"%s\"",
			alpha.GetEnableCommand(featureLlm))
	}
	return nil
}

func NewManager(
	alphaManager *alpha.FeatureManager,
) Manager {
	return Manager{
		alphaManager: alphaManager,
	}
}

// Manager provides functionality to manage Language Model (LLM) features and capabilities.
// It encapsulates the alpha feature manager to control access to experimental LLM features.
type Manager struct {
	alphaManager *alpha.FeatureManager
}

type LlmType string

func (l LlmType) String() string {
	switch l {
	case LlmTypeOllama:
		return "Ollama"
	case LlmTypeOpenAIAzure:
		return "OpenAI Azure"
	default:
		return string(l)
	}
}

const (
	LlmTypeOpenAIAzure LlmType = "azure"
	LlmTypeOllama      LlmType = "ollama"
)

// LlmModel represents a language model with its name and version information.
// Name specifies the identifier of the language model.
// Version indicates the specific version or release of the model.
type LlmModel struct {
	Name    string
	Version string
}

// InfoResponse represents the configuration information of a Language Learning Model (LLM).
// It contains details about the model type, deployment location, model specification,
// and endpoint URL for remote models.
type InfoResponse struct {
	Type    LlmType
	IsLocal bool
	Model   LlmModel
	Url     string // For remote models, this is the API endpoint URL
}

// NotEnabledError represents an error that occurs when LLM functionality is not enabled.
// This error is typically raised when attempting to use LLM features that have not been
// activated or configured in the system.
type NotEnabledError struct {
}

func (e NotEnabledError) Error() string {
	return fmt.Sprintf("LLM feature is not enabled. Run '%s' to enable",
		alpha.GetEnableCommand(featureLlm))
}

// InvalidLlmConfiguration represents an error that occurs when the LLM (Large Language Model)
// configuration is invalid or improperly formatted. This error type is used to indicate
// configuration-related issues in the LLM system.
type InvalidLlmConfiguration struct {
}

func (e InvalidLlmConfiguration) Error() string {
	return "Unable to determine LLM configuration. Please check your environment variables or configuration."
}

// Info obtains configuration information about the LLM (Large Language Model) feature.
// If the LLM feature is not enabled through the alpha manager, it returns a NotEnabledError.
// The function writes output to the provided stdout writer.
// Returns an InfoResponse containing the LLM configuration and any error that occurred.
func (m Manager) Info(stdout io.Writer) (InfoResponse, error) {
	if !m.alphaManager.IsEnabled(featureLlm) {
		return InfoResponse{}, NotEnabledError{}
	}
	return LlmConfig()
}

var availableLlmTypes = []LlmType{
	LlmTypeOpenAIAzure,
	LlmTypeOllama,
}

// LlmConfig attempts to load and validate LLM (Language Learning Model) configuration.
// It first determines the default LLM type, which can be overridden by the AZD_LLM_TYPE
// environment variable. It then tries to load configurations for available LLM types
// in order, starting with the default type.
//
// The function supports two LLM types:
// - LlmTypeOpenAIAzure (default)
// - LlmTypeOllama
//
// Returns:
//   - InfoResponse: Contains the successfully loaded LLM configuration
//   - error: Returns an error if no valid LLM configuration could be loaded or if
//     an unknown LLM type is specified in AZD_LLM_TYPE
func LlmConfig() (InfoResponse, error) {
	defaultLLm := LlmTypeOpenAIAzure
	// Default LLM can be overridden by environment variable AZD_LLM_TYPE
	if value, isDefined := os.LookupEnv("AZD_LLM_TYPE"); isDefined {
		switch strings.ToLower(value) {
		case string(LlmTypeOllama):
			defaultLLm = LlmTypeOllama
		case string(LlmTypeOpenAIAzure):
			defaultLLm = LlmTypeOpenAIAzure
		default:
			return InfoResponse{}, fmt.Errorf("unknown LLM type: %s", value)
		}
	}

	// keep default on the top and add the rest in the order they are defined
	configOrder := []LlmType{defaultLLm}
	for _, llmType := range availableLlmTypes {
		if llmType != defaultLLm {
			configOrder = append(configOrder, llmType)
		}
	}

	for _, llmType := range configOrder {
		log.Println("Checking LLM configuration for: ", llmType)
		info, err := loadLlmConfig(llmType)
		if err != nil {
			log.Printf("Failed to load LLM configuration for %s: %v\n", llmType, err)
			continue // Try the next LLM type
		}
		return info, nil
	}

	return InfoResponse{}, InvalidLlmConfiguration{}
}

// loadLlmConfig loads the configuration for the specified LLM type.
// It returns an InfoResponse containing the LLM configuration details and any error encountered.
//
// Parameters:
//   - llmType: The type of LLM to load configuration for (LlmTypeOllama or LlmTypeOpenAIAzure)
//
// Returns:
//   - InfoResponse: Configuration details for the specified LLM
//   - error: InvalidLlmConfiguration error if an unsupported LLM type is provided
func loadLlmConfig(llmType LlmType) (InfoResponse, error) {
	switch llmType {
	case LlmTypeOllama:
		return loadOllama()
	case LlmTypeOpenAIAzure:
		return loadAzureOpenAi()
	default:
		return InfoResponse{}, InvalidLlmConfiguration{}
	}
}

// LlmClient creates and returns a new LLM (Language Learning Model) client based on the provided InfoResponse.
// It supports different types of LLM services including Ollama and Azure OpenAI.
//
// Parameters:
//   - info: InfoResponse containing the configuration details for the LLM service
//
// Returns:
//   - Client: A configured LLM client wrapper
//   - error: An error if the client creation fails or if the LLM type is unsupported
func LlmClient(info InfoResponse) (Client, error) {
	switch info.Type {
	case LlmTypeOllama:
		c, err := ollama.New(ollama.WithModel(info.Model.Name))
		return Client{
			Model: c,
		}, err
	case LlmTypeOpenAIAzure:
		c, err := openai.New(
			openai.WithModel(info.Model.Name),
			openai.WithAPIType(openai.APITypeAzure),
			openai.WithAPIVersion(info.Model.Version),
			openai.WithBaseURL(info.Url),
		)
		return Client{
			Model: c,
		}, err
	default:
		return Client{}, fmt.Errorf("unsupported LLM type: %s", info.Type)
	}
}
