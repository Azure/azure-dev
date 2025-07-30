// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"azd.ai.start/internal/logging"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/tmc/langchaingo/llms/openai"
)

func NewRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "azd ai.chat <command> [options]",
		Short:         "Enables interactive AI agent through AZD",
		SilenceUsage:  true,
		SilenceErrors: true,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAIAgent(cmd.Context(), args)
		},
	}

	return rootCmd
}

type AiModelConfig struct {
	Endpoint       string `json:"endpoint"`
	ApiKey         string `json:"apiKey"`
	DeploymentName string `json:"deploymentName"`
}

// runAIAgent creates and runs the enhanced AI agent using LangChain Go
func runAIAgent(ctx context.Context, args []string) error {
	// Create a new context that includes the AZD access token
	ctx = azdext.WithAccessToken(ctx)

	// Create a new AZD client
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return fmt.Errorf("failed to create azd client: %w", err)
	}

	defer azdClient.Close()

	getSectionResponse, err := azdClient.
		UserConfig().
		GetSection(ctx, &azdext.GetUserConfigSectionRequest{
			Path: "ai.chat.model",
		})
	if err != nil {
		return fmt.Errorf("AI model configuration not found, %w", err)
	}

	var aiConfig *AiModelConfig
	if err := json.Unmarshal(getSectionResponse.Section, &aiConfig); err != nil {
		return fmt.Errorf("failed to unmarshal AI model configuration: %w", err)
	}

	_, _ = azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{
			Message: "Ready?",
		},
	})

	// Common deployment names to try
	azureAPIVersion := "2024-02-15-preview"

	var llm *openai.LLM

	// Try different deployment names
	if aiConfig.Endpoint != "" && aiConfig.ApiKey != "" {
		// Use Azure OpenAI with proper configuration
		fmt.Printf("🔵 Trying Azure OpenAI with deployment: %s\n", aiConfig.DeploymentName)

		actionLogger := logging.NewActionLogger(
			logging.WithDebug(false),
		)

		llm, err = openai.New(
			openai.WithToken(aiConfig.ApiKey),
			openai.WithBaseURL(aiConfig.Endpoint+"/"),
			openai.WithAPIType(openai.APITypeAzure),
			openai.WithAPIVersion(azureAPIVersion),
			openai.WithModel(aiConfig.DeploymentName),
			openai.WithCallback(actionLogger),
		)

		if err == nil {
			fmt.Printf("✅ Successfully connected with deployment: %s\n", aiConfig.DeploymentName)
		} else {
			fmt.Printf("❌ Failed with deployment %s: %v\n", aiConfig.DeploymentName, err)
		}
	}

	if llm == nil {
		return fmt.Errorf("failed to connect to any Azure OpenAI deployment")
	}

	// Use the enhanced Azure AI agent with full capabilities
	return RunEnhancedAzureAgent(ctx, llm, args)
}
