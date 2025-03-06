// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.ai.builder/internal/pkg/qna"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type scenarioInput struct {
	SelectedScenario string                `json:"selectedScenario,omitempty"`
	Rag              ragScenarioInput      `json:"rag,omitempty"`
	Agent            AgentScenarioInput    `json:"agent,omitempty"`
	ModelExploration modelExplorationInput `json:"modelExploration,omitempty"`
}

type ragScenarioInput struct {
	UseCustomData    bool     `json:"useCustomData,omitempty"`
	DataTypes        []string `json:"dataTypes,omitempty"`
	DataLocations    []string `json:"dataLocations,omitempty"`
	InteractionTypes []string `json:"interactionTypes,omitempty"`
	ModelSelection   string   `json:"modelSelection,omitempty"`
}

type AgentScenarioInput struct {
	Tasks            []string `json:"tasks,omitempty"`
	DataTypes        []string `json:"dataTypes,omitempty"`
	InteractionTypes []string `json:"interactionTypes,omitempty"`
}

type modelExplorationInput struct {
	Tasks []string `json:"tasks,omitempty"`
}

func newStartCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Get the context of the AZD project & environment.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create a new context that includes the AZD access token
			ctx := azdext.WithAccessToken(cmd.Context())

			// Create a new AZD client
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}

			defer azdClient.Close()

			scenarioData := scenarioInput{}

			// Build up list of questions
			config := qna.DecisionTreeConfig{
				Questions: map[string]qna.Question{
					"root": {
						Text:    "What type of AI scenario are you building?",
						Type:    qna.SingleSelect,
						Binding: &scenarioData.SelectedScenario,
						Choices: []qna.Choice{
							{Label: "RAG Application (Retrieval-Augmented Generation)", Value: "rag"},
							{Label: "AI Agent", Value: "agent"},
							{Label: "Explore AI Models", Value: "ai-model"},
						},
						Branches: map[any]string{
							"rag":      "rag-data-type",
							"agent":    "agent-tasks",
							"ai-model": "model-exploration",
						},
					},
					"rag-custom-data": {
						Text:    "Does your application require custom data?",
						Type:    qna.BooleanInput,
						Binding: &scenarioData.Rag.UseCustomData,
						Branches: map[any]string{
							true:  "rag-data-type",
							false: "rag-user-interaction",
						},
					},
					"rag-data-type": {
						Text:    "What type of data are you using?",
						Type:    qna.MultiSelect,
						Binding: &scenarioData.Rag.DataTypes,
						Choices: []qna.Choice{
							{Label: "Structured documents, ex. JSON, CSV", Value: "structured-documents"},
							{Label: "Unstructured documents, ex. PDF, Word", Value: "unstructured-documents"},
							{Label: "Videos", Value: "videos"},
							{Label: "Images", Value: "images"},
							{Label: "Audio", Value: "audio"},
						},
						Branches: map[any]string{
							"*": "rag-data-location",
						},
					},
					"rag-data-location": {
						Text:    "Where is your data located?",
						Type:    qna.MultiSelect,
						Binding: &scenarioData.Rag.DataLocations,
						Choices: []qna.Choice{
							{Label: "Azure Blob Storage", Value: "rag-blob-storage"},
							{Label: "Azure SQL Database", Value: "rag-databases"},
							{Label: "Local file system", Value: "rag-local-file-system"},
						},
						Branches: map[any]string{
							"*": "rag-user-interaction",
						},
					},
					"rag-user-interaction": {
						Text:    "How do you want users to interact with the data?",
						Type:    qna.MultiSelect,
						Binding: &scenarioData.Rag.InteractionTypes,
						Choices: []qna.Choice{
							{Label: "Chatbot", Value: "rag-chatbot"},
							{Label: "Web Application", Value: "rag-web"},
							{Label: "Mobile Application", Value: "rag-mobile-app"},
						},
						Branches: map[any]string{
							"*": "rag-model-selection",
						},
					},
					"rag-model-selection": {
						Text:    "Do you know which models you want to use?",
						Type:    qna.SingleSelect,
						Binding: &scenarioData.Rag.ModelSelection,
						Choices: []qna.Choice{
							{Label: "Choose for me", Value: "rag-choose-model"},
							{Label: "Help me choose", Value: "rag-guide-model"},
							{Label: "Yes, I have some models in mind", Value: "rag-user-models"},
						},
					},
					"agent-tasks": {
						Text:    "What tasks do you want the AI agent to perform?",
						Type:    qna.MultiSelect,
						Binding: &scenarioData.Agent.Tasks,
						Choices: []qna.Choice{
							{Label: "Custom Function Calling", Value: "custom-function-calling"},
							{Label: "Integrate with Open API based services", Value: "openapi"},
							{Label: "Run Azure Functions", Value: "azure-functions"},
						},
						Branches: map[any]string{
							"*": "agent-data-types",
						},
					},
					"agent-data-types": {
						Text:    "Where will the agent retrieve it's data from?",
						Type:    qna.MultiSelect,
						Binding: &scenarioData.Agent.DataTypes,
						Choices: []qna.Choice{
							{Label: "Azure Blob Storage", Value: "agent-blob-storage"},
							{Label: "Azure SQL Database", Value: "agent-databases"},
							{Label: "Local file system", Value: "agent-local-file-system"},
						},
						Branches: map[any]string{
							"*": "agent-interaction",
						},
					},
					"agent-interaction": {
						Text:    "How do you want users to interact with the agent?",
						Type:    qna.MultiSelect,
						Binding: &scenarioData.Agent.InteractionTypes,
						Choices: []qna.Choice{
							{Label: "Chatbot", Value: "agent-chatbot"},
							{Label: "Web Application", Value: "agent-web"},
							{Label: "Mobile Application", Value: "agent-mobile-app"},
							{Label: "Message Queue", Value: "agent-message-queue"},
						},
					},
					"model-exploration": {
						Text:    "What types of tasks should the AI models perform?",
						Type:    qna.MultiSelect,
						Binding: &scenarioData.ModelExploration.Tasks,
						Choices: []qna.Choice{
							{Label: "Text Generation", Value: "text-generation"},
							{Label: "Image Generation", Value: "image-generation"},
							{Label: "Audio Generation", Value: "audio-generation"},
							{Label: "Video Generation", Value: "video-generation"},
							{Label: "Text Classification", Value: "text-classification"},
							{Label: "Image Classification", Value: "image-classification"},
							{Label: "Audio Classification", Value: "audio-classification"},
							{Label: "Video Classification", Value: "video-classification"},
							{Label: "Text Summarization", Value: "text-summarization"},
						},
					},
				},
			}

			fmt.Println("Welcome to the AI Builder CLI!")
			fmt.Println("This tool will help you build an AI scenario using Azure services.")
			fmt.Println("Please answer the following questions to get started.")
			fmt.Println()

			decisionTree := qna.NewDecisionTree(azdClient, config)
			if err := decisionTree.Run(ctx); err != nil {
				return fmt.Errorf("failed to run decision tree: %w", err)
			}

			jsonBytes, err := json.MarshalIndent(scenarioData, "", "    ")
			if err != nil {
				return fmt.Errorf("failed to marshal scenario data: %w", err)
			}

			fmt.Println()
			fmt.Println("Captured scenario data:")
			fmt.Println()
			fmt.Println(string(jsonBytes))

			return nil
		},
	}
}
