// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.ai.builder/internal/pkg/azure/ai"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.ai.builder/internal/pkg/qna"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/spf13/cobra"
)

type scenarioInput struct {
	SelectedScenario string `json:"selectedScenario,omitempty"`

	UseCustomData       bool     `json:"useCustomData,omitempty"`
	DataTypes           []string `json:"dataTypes,omitempty"`
	DataLocations       []string `json:"dataLocations,omitempty"`
	InteractionTypes    []string `json:"interactionTypes,omitempty"`
	ModelSelection      string   `json:"modelSelection,omitempty"`
	LocalFilePath       string   `json:"localFilePath,omitempty"`
	LocalFileSelection  string   `json:"localFileSelection,omitempty"`
	LocalFileGlobFilter string   `json:"localFileGlobFilter,omitempty"`
	DatabaseType        string   `json:"databaseType,omitempty"`
	StorageAccountId    string   `json:"storageAccountId,omitempty"`
	DatabaseId          string   `json:"databaseId,omitempty"`
	MessagingType       string   `json:"messageType,omitempty"`
	MessagingId         string   `json:"messagingId,omitempty"`
	ModelTasks          []string `json:"modelTasks,omitempty"`
	AppType             string   `json:"appType,omitempty"`
	AppId               string   `json:"appId,omitempty"`
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

			credential, err := azidentity.NewAzureDeveloperCLICredential(nil)
			if err != nil {
				return fmt.Errorf("Run `azd auth login` to login", err)
			}

			modelCatalog := ai.NewModelCatalog(credential)
			var aiModelCatalog []*ai.AiModel

			spinner := ux.NewSpinner(&ux.SpinnerOptions{
				Text: "Loading AI Model Catalog",
			})
			err = spinner.Run(ctx, func(ctx context.Context) error {
				credential, err := azidentity.NewAzureDeveloperCLICredential(nil)
				if err != nil {
					return err
				}

				var loadErr error
				modelCatalog = ai.NewModelCatalog(credential)
				aiModelCatalog, loadErr = modelCatalog.ListAllModels(ctx, azureContext.Scope.SubscriptionId)
				if loadErr != nil {
					return err
				}

				return nil
			})
			if err != nil {
				return fmt.Errorf("failed to load AI model catalog: %w", err)
			}

			welcomeMessage := []string{
				"This tool will help you build an AI scenario using Azure services.",
				"Please answer the following questions to get started.",
			}

			// Build up list of questions
			config := qna.DecisionTreeConfig{
				Questions: map[string]qna.Question{
					"root": {
						Binding: &scenarioData.SelectedScenario,
						Heading: "Welcome to the AI Builder Extension!",
						Message: strings.Join(welcomeMessage, "\n"),
						Prompt: &qna.SingleSelectPrompt{
							Client:          azdClient,
							Message:         "What type of AI scenario are you building?",
							HelpMessage:     "Choose the scenario that best fits your needs.",
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
							"ai-model": "model-capabilities",
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
							HelpMessage:  "Custom data is data that is not publicly available and is specific to your application.",
							DefaultValue: to.Ptr(true),
						},
					},
					"choose-data-types": {
						Binding: &scenarioData.DataTypes,
						Heading: "Data Sources",
						Message: "Lets identify all the data source that will be used in your application.",
						Prompt: &qna.MultiSelectPrompt{
							Client:          azdClient,
							Message:         "What type of data are you using?",
							HelpMessage:     "Select all the data types that apply to your application.",
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
							HelpMessage:     "Select all the data locations that apply to your application.",
							EnableFiltering: to.Ptr(false),
							Choices: []qna.Choice{
								{Label: "Azure Blob Storage", Value: "blob-storage"},
								{Label: "Azure Database", Value: "databases"},
								{Label: "Local file system", Value: "local-file-system"},
								{Label: "Other", Value: "other-datasource"},
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
							HelpMessage:             "You can select an existing storage account or create a new one.",
							AzureContext:            azureContext,
						},
					},
					"choose-database-type": {
						Binding: &scenarioData.DatabaseType,
						Prompt: &qna.SingleSelectPrompt{
							Message:         "Which type of database?",
							HelpMessage:     "Select the type of database that best fits your needs.",
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
							HelpMessage:  "You can select an existing database or create a new one.",
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
							HelpMessage: "This path can be absolute or relative to the current working directory. " +
								"Please make sure the path is accessible from the machine running this command.",
							Placeholder: "./data",
						},
						Next: "local-file-choose-files",
					},
					"local-file-choose-files": {
						Binding: &scenarioData.LocalFileSelection,
						Prompt: &qna.SingleSelectPrompt{
							Client:          azdClient,
							Message:         "Which files?",
							HelpMessage:     "You can select all files or use a glob expression to filter the files.",
							EnableFiltering: to.Ptr(false),
							Choices: []qna.Choice{
								{Label: "All Files", Value: "all-files"},
								{Label: "Glob Expression", Value: "glob-expression"},
							},
						},
						Branches: map[any]string{
							"glob-expression": "local-file-glob",
						},
					},
					"local-file-glob": {
						Binding: &scenarioData.LocalFileGlobFilter,
						Prompt: &qna.TextPrompt{
							Client:  azdClient,
							Message: "Enter a glob expression to filter files",
							HelpMessage: "A glob expression is a string that uses wildcard characters to match file names. " +
								" For example, *.txt will match all text files in the current directory.",
							Placeholder: "*.json",
						},
					},
					"rag-user-interaction": {
						Binding: &scenarioData.InteractionTypes,
						Heading: "Application Hosting",
						Message: "Now we will figure out all the different ways users will interact with your application.",
						Prompt: &qna.MultiSelectPrompt{
							Client:          azdClient,
							Message:         "How do you want users to interact with the data?",
							HelpMessage:     "Select all the data interaction types that apply to your application.",
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
						Next: "start-choose-model",
					},
					"agent-interaction": {
						Binding: &scenarioData.InteractionTypes,
						Heading: "Agent Hosting",
						Message: "Now we will figure out all the different ways users and systems will interact with your agent.",
						Prompt: &qna.MultiSelectPrompt{
							Client:          azdClient,
							Message:         "How do you want users to interact with the agent?",
							HelpMessage:     "Select all the data interaction types that apply to your application.",
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
							"messaging": "choose-messaging-type",
						},
						AfterAsk: func(ctx context.Context, q *qna.Question, value any) error {
							q.State["interactionType"] = value
							return nil
						},
						Next: "start-choose-model",
					},
					"agent-tasks": {
						Binding: &scenarioData.ModelTasks,
						Prompt: &qna.MultiSelectPrompt{
							Client:          azdClient,
							Message:         "What tasks do you want the AI agent to perform?",
							HelpMessage:     "Select all the tasks that apply to your application.",
							EnableFiltering: to.Ptr(false),
							Choices: []qna.Choice{
								{Label: "Custom Function Calling", Value: "custom-function-calling"},
								{Label: "Integrate with Open API based services", Value: "openapi"},
								{Label: "Run Azure Functions", Value: "azure-functions"},
								{Label: "Other", Value: "other-model-tasks"},
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
							Client:          azdClient,
							EnableFiltering: to.Ptr(false),
							Choices: []qna.Choice{
								{Label: "Choose for me", Value: "choose-app"},
								{Label: "App Service (Coming Soon)", Value: "webapp"},
								{Label: "Container App", Value: "containerapp"},
								{Label: "Function App (Coming Soon)", Value: "functionapp"},
								{Label: "Static Web App (Coming Soon)", Value: "staticwebapp"},
								{Label: "Other", Value: "otherapp"},
							},
						},
						Branches: map[any]string{
							"webapp":       "choose-app-resource",
							"containerapp": "choose-app-resource",
							"functionapp":  "choose-app-resource",
							"staticwebapp": "choose-app-resource",
						},
					},
					"choose-app-resource": {
						Binding: &scenarioData.AppId,
						Prompt: &qna.SubscriptionResourcePrompt{
							HelpMessage:  "You can select an existing application or create a new one.",
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
							HelpMessage:     "Select the messaging service that best fits your needs.",
							EnableFiltering: to.Ptr(false),
							Choices: []qna.Choice{
								{Label: "Choose for me", Value: "choose-messaging"},
								{Label: "Azure Service Bus", Value: "messaging.eventhubs"},
								{Label: "Azure Event Hubs", Value: "messaging.servicebus"},
							},
						},
						Branches: map[any]string{
							"messaging.eventhubs":  "choose-messaging-resource",
							"messaging.servicebus": "choose-messaging-resource",
						},
					},
					"choose-messaging-resource": {
						Binding: &scenarioData.MessagingId,
						Prompt: &qna.SubscriptionResourcePrompt{
							HelpMessage:  "You can select an existing messaging service or create a new one.",
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
							HelpMessage: "Select all the tasks that apply to your application. " +
								"These tasks will help you narrow down the type of models you need.",
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
					"start-choose-model": {
						Prompt: &qna.SingleSelectPrompt{
							Client:          azdClient,
							Message:         "How do you want to find the right model?",
							HelpMessage:     "Select the option that best fits your needs.",
							EnableFiltering: to.Ptr(false),
							Choices: []qna.Choice{
								{Label: "Choose for me", Value: "choose-model"},
								{Label: "Help me choose", Value: "guide-model"},
								{Label: "I will choose model", Value: "user-model"},
							},
						},
						Branches: map[any]string{
							"guide-model": "guide-model-select",
							"user-model":  "user-model-select",
						},
					},
					"user-model-select": {
						Prompt: &qna.SingleSelectPrompt{
							BeforeAsk: func(ctx context.Context, q *qna.Question, p *qna.SingleSelectPrompt) error {
								filteredModels := modelCatalog.ListFilteredModels(ctx, aiModelCatalog, nil)

								choices := make([]qna.Choice, len(filteredModels))
								for i, model := range aiModelCatalog {
									choices[i] = qna.Choice{
										Label: model.Name,
										Value: model.Name,
									}
								}
								p.Choices = choices

								return nil
							},
							Client:      azdClient,
							Message:     "Which model do you want to use?",
							HelpMessage: "Select the model that best fits your needs.",
						},
					},
					"guide-model-select": {
						Prompt: &qna.MultiSelectPrompt{
							Client:  azdClient,
							Message: "Filter AI Models",
							Choices: []qna.Choice{
								{Label: "Filter by capabilities", Value: "filter-model-capability"},
								{Label: "Filter by creator", Value: "filter-model-format"},
								{Label: "Filter by status", Value: "filter-model-status"},
							},
						},
						Branches: map[any]string{
							"filter-model-capability": "filter-model-capability",
							"filter-model-format":     "filter-model-format",
							"filter-model-status":     "filter-model-status",
						},
						Next: "user-model-select",
					},
					"filter-model-capability": {
						Prompt: &qna.MultiSelectPrompt{
							Client:      azdClient,
							Message:     "What capabilities do you want the model to have?",
							HelpMessage: "Select all the capabilities that apply to your application.",
							Choices: []qna.Choice{
								{Label: "Audio", Value: "audio"},
								{Label: "Chat Completion", Value: "chatCompletion"},
								{Label: "Text Completion", Value: "completion"},
								{Label: "Generate Vector Embeddings", Value: "embeddings"},
								{Label: "Image Generation", Value: "imageGenerations"},
							},
						},
						AfterAsk: func(ctx context.Context, q *qna.Question, value any) error {
							capabilities := value.([]string)
							q.State["capabilities"] = capabilities
							return nil
						},
					},
					"filter-model-format": {
						Prompt: &qna.MultiSelectPrompt{
							BeforeAsk: func(ctx context.Context, q *qna.Question, p *qna.MultiSelectPrompt) error {
								formats := modelCatalog.ListAllFormats(ctx, aiModelCatalog)
								choices := make([]qna.Choice, len(formats))
								for i, format := range formats {
									choices[i] = qna.Choice{
										Label: format,
										Value: format,
									}
								}
								p.Choices = choices
								return nil
							},
							Client:      azdClient,
							Message:     "Filter my by company or creator",
							HelpMessage: "Select all the companies or creators that apply to your application.",
						},
						AfterAsk: func(ctx context.Context, q *qna.Question, value any) error {
							formats := value.([]string)
							q.State["formats"] = formats
							return nil
						},
					},
					"filter-model-status": {
						Prompt: &qna.MultiSelectPrompt{
							BeforeAsk: func(ctx context.Context, q *qna.Question, p *qna.MultiSelectPrompt) error {
								statuses := modelCatalog.ListAllStatuses(ctx, aiModelCatalog)
								choices := make([]qna.Choice, len(statuses))
								for i, status := range statuses {
									choices[i] = qna.Choice{
										Label: status,
										Value: status,
									}
								}
								p.Choices = choices
								return nil
							},
							Client:          azdClient,
							Message:         "Filter by model release status?",
							HelpMessage:     "Select all the model release status that apply to your application.",
							EnableFiltering: to.Ptr(false),
						},
						AfterAsk: func(ctx context.Context, q *qna.Question, value any) error {
							statuses := value.([]string)
							q.State["status"] = statuses
							return nil
						},
					},
				},
			}

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
