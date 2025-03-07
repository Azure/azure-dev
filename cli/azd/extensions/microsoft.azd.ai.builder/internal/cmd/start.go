// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.ai.builder/internal/pkg/qna"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type scenarioInput struct {
	SelectedScenario string `json:"selectedScenario,omitempty"`

	UseCustomData    bool     `json:"useCustomData,omitempty"`
	DataTypes        []string `json:"dataTypes,omitempty"`
	DataLocations    []string `json:"dataLocations,omitempty"`
	InteractionTypes []string `json:"interactionTypes,omitempty"`
	ModelSelection   string   `json:"modelSelection,omitempty"`
	LocalFilePath    string   `json:"localFilePath,omitempty"`
	DatabaseType     string   `json:"databaseType,omitempty"`
	StorageAccountId string   `json:"storageAccountId,omitempty"`
	DatabaseId       string   `json:"databaseId,omitempty"`
	MessagingType    string   `json:"messageType,omitempty"`
	MessagingId      string   `json:"messagingId,omitempty"`
	ModelTasks       []string `json:"modelTasks,omitempty"`
	AppType          string   `json:"appType,omitempty"`
	AppId            string   `json:"appId,omitempty"`
}

type resourceTypeConfig struct {
	ResourceType            string
	ResourceTypeDisplayName string
	Kinds                   []string
}

var (
	dbResourceMap = map[string]resourceTypeConfig{
		"db.cosmos": {
			ResourceType:            "Microsoft.DocumentDB/databaseAccounts",
			ResourceTypeDisplayName: "Cosmos Database Account",
			Kinds:                   []string{"GlobalDocumentDB"},
		},
		"db.mongo": {
			ResourceType:            "Microsoft.DocumentDB/databaseAccounts",
			ResourceTypeDisplayName: "MongoDB Database Account",
			Kinds:                   []string{"MongoDB"},
		},
		"db.postgres": {
			ResourceType:            "Microsoft.DBforPostgreSQL/flexibleServers",
			ResourceTypeDisplayName: "PostgreSQL Server",
		},
		"db.mysql": {
			ResourceType:            "Microsoft.DBforMySQL/flexibleServers",
			ResourceTypeDisplayName: "MySQL Server",
		},
		"db.redis": {
			ResourceType:            "Microsoft.Cache/Redis",
			ResourceTypeDisplayName: "Redis Cache",
		},
	}

	messagingResourceMap = map[string]resourceTypeConfig{
		"messaging.eventhubs": {
			ResourceType:            "Microsoft.EventHub/namespaces",
			ResourceTypeDisplayName: "Event Hub Namespace",
		},
		"messaging.servicebus": {
			ResourceType:            "Microsoft.ServiceBus/namespaces",
			ResourceTypeDisplayName: "Service Bus Namespace",
		},
	}

	appResourceMap = map[string]resourceTypeConfig{
		"webapp": {
			ResourceType:            "Microsoft.Web/sites",
			ResourceTypeDisplayName: "Web App",
			Kinds:                   []string{"app"},
		},
		"containerapp": {
			ResourceType:            "Microsoft.App/containerApps",
			ResourceTypeDisplayName: "Container App",
		},
		"functionapp": {
			ResourceType:            "Microsoft.Web/sites",
			ResourceTypeDisplayName: "Function App",
			Kinds:                   []string{"functionapp"},
		},
		"staticwebapp": {
			ResourceType:            "Microsoft.Web/staticSites",
			ResourceTypeDisplayName: "Static Web App",
		},
	}
)

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

			_, err = azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
			if err != nil {
				return fmt.Errorf("project not found. Run `azd init` to create a new project, %w", err)
			}

			envResponse, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
			if err != nil {
				return fmt.Errorf("environment not found. Run `azd env new` to create a new environment, %w", err)
			}

			envValues, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
				Name: envResponse.Environment.Name,
			})
			if err != nil {
				return fmt.Errorf("failed to get environment values: %w", err)
			}

			envValueMap := make(map[string]string)
			for _, value := range envValues.KeyValues {
				envValueMap[value.Key] = value.Value
			}

			azureContext := &azdext.AzureContext{
				Scope: &azdext.AzureScope{
					SubscriptionId: envValueMap["AZURE_SUBSCRIPTION_ID"],
				},
				Resources: []string{},
			}

			scenarioData := scenarioInput{}

			// Build up list of questions
			config := qna.DecisionTreeConfig{
				Questions: map[string]qna.Question{
					"root": {
						Binding: &scenarioData.SelectedScenario,
						Prompt: &qna.SingleSelectPrompt{
							Client:          azdClient,
							Message:         "What type of AI scenario are you building?",
							EnableFiltering: to.Ptr(false),
							Choices: []qna.Choice{
								{Label: "RAG Application (Retrieval-Augmented Generation)", Value: "rag"},
								{Label: "AI Agent", Value: "agent"},
								{Label: "Explore AI Models", Value: "ai-model"},
							},
						},
						Branches: map[any]string{
							"rag":      "use-custom-data",
							"agent":    "agent-tasks",
							"ai-model": "model-exploration",
						},
					},
					"use-custom-data": {
						Binding: &scenarioData.UseCustomData,
						BeforeAsk: func(ctx context.Context, q *qna.Question) error {
							switch scenarioData.SelectedScenario {
							case "rag":
								q.Branches = map[any]string{
									true:  "choose-data-types",
									false: "rag-user-interaction",
								}
							case "agent":
								q.Branches = map[any]string{
									true:  "choose-data-types",
									false: "agent-tasks",
								}
							}

							return nil
						},
						Prompt: &qna.ConfirmPrompt{
							Client:       azdClient,
							Message:      "Does your application require custom data?",
							DefaultValue: to.Ptr(true),
						},
					},
					"choose-data-types": {
						Binding: &scenarioData.DataTypes,
						Prompt: &qna.MultiSelectPrompt{
							Client:          azdClient,
							Message:         "What type of data are you using?",
							EnableFiltering: to.Ptr(false),
							Choices: []qna.Choice{
								{Label: "Structured documents, ex. JSON, CSV", Value: "structured-documents"},
								{Label: "Unstructured documents, ex. PDF, Word", Value: "unstructured-documents"},
								{Label: "Videos", Value: "videos"},
								{Label: "Images", Value: "images"},
								{Label: "Audio", Value: "audio"},
							},
						},
						Next: "data-location",
					},
					"data-location": {
						Binding: &scenarioData.DataLocations,
						Prompt: &qna.MultiSelectPrompt{
							Client:          azdClient,
							Message:         "Where is your data located?",
							EnableFiltering: to.Ptr(false),
							Choices: []qna.Choice{
								{Label: "Azure Blob Storage", Value: "blob-storage"},
								{Label: "Azure Database", Value: "databases"},
								{Label: "Local file system", Value: "local-file-system"},
							},
						},
						Branches: map[any]string{
							"blob-storage":      "choose-storage",
							"databases":         "choose-database-type",
							"local-file-system": "local-file-system",
						},
						BeforeAsk: func(ctx context.Context, q *qna.Question) error {
							switch scenarioData.SelectedScenario {
							case "rag":
								q.Next = "rag-user-interaction"
							case "agent":
								q.Next = "agent-interaction"
							}
							return nil
						},
					},
					"choose-storage": {
						Binding: &scenarioData.StorageAccountId,
						Prompt: &qna.SubscriptionResourcePrompt{
							Client:                  azdClient,
							ResourceType:            "Microsoft.Storage/storageAccounts",
							ResourceTypeDisplayName: "Storage Account",
							AzureContext:            azureContext,
						},
					},
					"choose-database-type": {
						Binding: &scenarioData.DatabaseType,
						Prompt: &qna.SingleSelectPrompt{
							Message:         "Which type of database?",
							Client:          azdClient,
							EnableFiltering: to.Ptr(false),
							Choices: []qna.Choice{
								{Label: "CosmosDB", Value: "db.cosmos"},
								{Label: "PostgreSQL", Value: "db.postgres"},
								{Label: "MySQL", Value: "db.mysql"},
								{Label: "Redis", Value: "db.redis"},
								{Label: "MongoDB", Value: "db.mongo"},
							},
						},
						Next: "choose-database-resource",
					},
					"choose-database-resource": {
						Binding: &scenarioData.DatabaseId,
						Prompt: &qna.SubscriptionResourcePrompt{
							Client:       azdClient,
							AzureContext: azureContext,
							BeforeAsk: func(ctx context.Context, q *qna.Question, p *qna.SubscriptionResourcePrompt) error {
								resourceType, has := dbResourceMap[scenarioData.DatabaseType]
								if !has {
									return fmt.Errorf(
										"unknown resource type for database: %s",
										scenarioData.DatabaseType,
									)
								}

								p.ResourceType = resourceType.ResourceType
								p.Kinds = resourceType.Kinds
								p.ResourceTypeDisplayName = resourceType.ResourceTypeDisplayName

								return nil
							},
						},
					},
					"local-file-system": {
						Binding: &scenarioData.LocalFilePath,
						Prompt: &qna.TextPrompt{
							Client:  azdClient,
							Message: "Path to the local files",
						},
					},
					"rag-user-interaction": {
						Binding: &scenarioData.InteractionTypes,
						Prompt: &qna.MultiSelectPrompt{
							Client:          azdClient,
							Message:         "How do you want users to interact with the data?",
							EnableFiltering: to.Ptr(false),
							Choices: []qna.Choice{
								{Label: "Chatbot", Value: "chatbot"},
								{Label: "Web Application", Value: "webapp"},
							},
						},
						Branches: map[any]string{
							"chatbot": "choose-app-type",
							"webapp":  "choose-app-type",
						},
						AfterAsk: func(ctx context.Context, q *qna.Question, value any) error {
							q.State["interactionType"] = value
							return nil
						},
						Next: "model-selection",
					},
					"agent-interaction": {
						Binding: &scenarioData.InteractionTypes,
						Prompt: &qna.MultiSelectPrompt{
							Client:          azdClient,
							Message:         "How do you want users to interact with the agent?",
							EnableFiltering: to.Ptr(false),
							Choices: []qna.Choice{
								{Label: "Chatbot", Value: "chatbot"},
								{Label: "Web Application", Value: "webapp"},
								{Label: "Message Queue", Value: "messaging"},
							},
						},
						Branches: map[any]string{
							"chatbot":   "choose-app-type",
							"webapp":    "choose-app-type",
							"messaging": "choose-app-type",
						},
						AfterAsk: func(ctx context.Context, q *qna.Question, value any) error {
							q.State["interactionType"] = value
							return nil
						},
						Next: "model-selection",
					},
					"model-selection": {
						Binding: &scenarioData.ModelSelection,
						Prompt: &qna.SingleSelectPrompt{
							Client:          azdClient,
							Message:         "Do you want to know what type(s) of model(s) you would like to use?",
							EnableFiltering: to.Ptr(false),
							Choices: []qna.Choice{
								{Label: "Choose for me", Value: "choose-model"},
								{Label: "Help me choose", Value: "guide-model"},
								{Label: "Yes, I have some models in mind", Value: "user-models"},
							},
						},
					},
					"agent-tasks": {
						Binding: &scenarioData.ModelTasks,
						Prompt: &qna.MultiSelectPrompt{
							Client:          azdClient,
							Message:         "What tasks do you want the AI agent to perform?",
							EnableFiltering: to.Ptr(false),
							Choices: []qna.Choice{
								{Label: "Custom Function Calling", Value: "custom-function-calling"},
								{Label: "Integrate with Open API based services", Value: "openapi"},
								{Label: "Run Azure Functions", Value: "azure-functions"},
							},
						},
						Next: "use-custom-data",
					},
					"choose-app-type": {
						Binding: &scenarioData.AppType,
						Prompt: &qna.SingleSelectPrompt{
							BeforeAsk: func(ctx context.Context, q *qna.Question, p *qna.SingleSelectPrompt) error {
								appName := q.State["interactionType"].(string)
								p.Message = fmt.Sprintf(
									"Which type of application do you want to build for %s?",
									appName,
								)
								return nil
							},
							Client: azdClient,
							Choices: []qna.Choice{
								{Label: "App Service", Value: "webapp"},
								{Label: "Container App", Value: "containerapp"},
								{Label: "Function App", Value: "functionapp"},
								{Label: "Static Web App", Value: "staticwebapp"},
							},
						},
						Next: "choose-app-resource",
					},
					"choose-app-resource": {
						Binding: &scenarioData.AppId,
						Prompt: &qna.SubscriptionResourcePrompt{
							Client:       azdClient,
							AzureContext: azureContext,
							BeforeAsk: func(ctx context.Context, q *qna.Question, p *qna.SubscriptionResourcePrompt) error {
								resourceType, has := appResourceMap[scenarioData.AppType]
								if !has {
									return fmt.Errorf(
										"unknown resource type for database: %s",
										scenarioData.AppType,
									)
								}

								p.ResourceType = resourceType.ResourceType
								p.Kinds = resourceType.Kinds
								p.ResourceTypeDisplayName = resourceType.ResourceTypeDisplayName

								return nil
							},
						},
					},
					"choose-messaging-type": {
						Binding: &scenarioData.MessagingType,
						Prompt: &qna.SingleSelectPrompt{
							Client:          azdClient,
							Message:         "Which messaging service do you want to use?",
							EnableFiltering: to.Ptr(false),
							Choices: []qna.Choice{
								{Label: "Azure Service Bus", Value: "messaging.eventhubs"},
								{Label: "Azure Event Hubs", Value: "messaging.servicebus"},
							},
						},
						Next: "choose-messaging-resource",
					},
					"choose-messaging-resource": {
						Binding: &scenarioData.MessagingId,
						Prompt: &qna.SubscriptionResourcePrompt{
							Client:       azdClient,
							AzureContext: azureContext,
							BeforeAsk: func(ctx context.Context, q *qna.Question, p *qna.SubscriptionResourcePrompt) error {
								resourceType, has := messagingResourceMap[scenarioData.MessagingType]
								if !has {
									return fmt.Errorf(
										"unknown resource type for database: %s",
										scenarioData.MessagingType,
									)
								}

								p.ResourceType = resourceType.ResourceType
								p.Kinds = resourceType.Kinds
								p.ResourceTypeDisplayName = resourceType.ResourceTypeDisplayName

								return nil
							},
						},
					},
					"model-exploration": {
						Binding: &scenarioData.ModelTasks,
						Prompt: &qna.MultiSelectPrompt{
							Client:  azdClient,
							Message: "What type of tasks should the AI models perform?",
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
