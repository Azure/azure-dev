// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path" // added for POSIX path joining
	"path/filepath"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.ai.builder/internal/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.ai.builder/internal/pkg/azure/ai"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.ai.builder/internal/pkg/qna"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.ai.builder/internal/pkg/util"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.ai.builder/internal/resources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/spf13/cobra"
)

const (
	defaultChatCompletionModel = "gpt-4o"
	defaultEmbeddingModel      = "text-embedding-3-small"
)

type scenarioInput struct {
	SelectedScenario string `json:"selectedScenario,omitempty"`

	UseCustomData       bool     `json:"useCustomData,omitempty"`
	DataTypes           []string `json:"dataTypes,omitempty"`
	DataLocations       []string `json:"dataLocations,omitempty"`
	InteractionTypes    []string `json:"interactionTypes,omitempty"`
	LocalFilePath       string   `json:"localFilePath,omitempty"`
	LocalFileSelection  string   `json:"localFileSelection,omitempty"`
	LocalFileGlobFilter string   `json:"localFileGlobFilter,omitempty"`
	DatabaseType        string   `json:"databaseType,omitempty"`
	StorageAccountId    string   `json:"storageAccountId,omitempty"`
	DatabaseId          string   `json:"databaseId,omitempty"`
	MessagingType       string   `json:"messagingType,omitempty"`
	MessagingId         string   `json:"messagingId,omitempty"`
	ModelTasks          []string `json:"modelTasks,omitempty"`
	ModelSelections     []string `json:"modelSelections,omitempty"`
	AppHostTypes        []string `json:"appHostTypes,omitempty"`
	AppLanguages        []string `json:"appLanguages,omitempty"`
	AppResourceIds      []string `json:"appResourceIds,omitempty"`
	VectorStoreType     string   `json:"vectorStoreType,omitempty"`
	VectorStoreId       string   `json:"vectorStoreId,omitempty"`
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

	vectorStoreMap = map[string]resourceTypeConfig{
		"ai.search": {
			ResourceType:            "Microsoft.Search/searchServices",
			ResourceTypeDisplayName: "AI Search",
		},
		"db.cosmos": {
			ResourceType:            "Microsoft.DocumentDB/databaseAccounts",
			ResourceTypeDisplayName: "Cosmos Database Account",
			Kinds:                   []string{"GlobalDocumentDB"},
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
		"host.webapp": {
			ResourceType:            "Microsoft.Web/sites",
			ResourceTypeDisplayName: "Web App",
			Kinds:                   []string{"app"},
		},
		"host.containerapp": {
			ResourceType:            "Microsoft.App/containerApps",
			ResourceTypeDisplayName: "Container App",
		},
		"host.functionapp": {
			ResourceType:            "Microsoft.Web/sites",
			ResourceTypeDisplayName: "Function App",
			Kinds:                   []string{"functionapp"},
		},
		"host.staticwebapp": {
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

			fmt.Println()
			fmt.Println(output.WithHintFormat("Welcome to the AI Builder!"))
			fmt.Println("This tool will help you build an AI scenario using Azure services.")
			fmt.Println()

			azureContext, projectConfig, err := ensureAzureContext(ctx, azdClient)
			if err != nil {
				return fmt.Errorf("failed to ensure azure context: %w", err)
			}

			credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
				TenantID:                   azureContext.Scope.TenantId,
				AdditionallyAllowedTenants: []string{"*"},
			})
			if err != nil {
				return fmt.Errorf("failed to create azure credential: %w", err)
			}

			action := &startAction{
				azdClient:           azdClient,
				credential:          credential,
				azureContext:        azureContext,
				azureClient:         azure.NewAzureClient(credential),
				modelCatalogService: ai.NewModelCatalogService(credential),
				projectConfig:       projectConfig,
				scenarioData:        &scenarioInput{},
			}

			if err := action.Run(ctx, args); err != nil {
				return fmt.Errorf("failed to run start action: %w", err)
			}

			return nil
		},
	}
}

type startAction struct {
	credential          azcore.TokenCredential
	azdClient           *azdext.AzdClient
	azureContext        *azdext.AzureContext
	modelCatalogService *ai.ModelCatalogService
	azureClient         *azure.AzureClient
	projectConfig       *azdext.ProjectConfig
	scenarioData        *scenarioInput
	modelCatalog        map[string]*ai.AiModel
}

func (a *startAction) Run(ctx context.Context, args []string) error {
	// Build up list of questions
	decisionTree := qna.NewDecisionTree(a.createQuestions())
	if err := decisionTree.Run(ctx); err != nil {
		return fmt.Errorf("failed to run decision tree: %w", err)
	}

	spinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text:        "Updating project configuration",
		ClearOnStop: true,
	})

	fmt.Println()
	if err := spinner.Start(ctx); err != nil {
		return fmt.Errorf("failed to start spinner: %w", err)
	}

	resourcesToAdd := []*azdext.ComposedResource{}
	servicesToAdd := []*azdext.ServiceConfig{}

	// Add host resources such as container apps.
	for i, appKey := range a.scenarioData.InteractionTypes {
		appType := a.scenarioData.AppHostTypes[i]
		if appType == "" || appType == "choose-app" {
			appType = "host.containerapp"
		}

		languageType := a.scenarioData.AppLanguages[i]

		appConfig := map[string]any{
			"port": 8080,
		}

		appConfigJson, err := json.Marshal(appConfig)
		if err != nil {
			return fmt.Errorf("failed to marshal app config: %w", err)
		}

		appResource := &azdext.ComposedResource{
			Name:   appKey,
			Type:   appType,
			Config: appConfigJson,
		}

		serviceConfig := &azdext.ServiceConfig{
			Name:         appKey,
			Language:     languageType,
			Host:         strings.ReplaceAll(appType, "host.", ""),
			RelativePath: filepath.Join("src", appKey),
		}

		servicesToAdd = append(servicesToAdd, serviceConfig)
		resourcesToAdd = append(resourcesToAdd, appResource)
	}

	// Add database resources
	if a.scenarioData.DatabaseType != "" {
		dbResource := &azdext.ComposedResource{
			Name: "database",
			Type: a.scenarioData.DatabaseType,
		}
		resourcesToAdd = append(resourcesToAdd, dbResource)
	}

	// if a.scenarioData.VectorStoreType != "" {
	// 	vectorStoreResource := &azdext.ComposedResource{
	// 		Name: "vectorStore",
	// 		Type: a.scenarioData.VectorStoreType,
	// 	}
	// 	resourcesToAdd = append(resourcesToAdd, vectorStoreResource)
	// }

	// Add storage resources
	if a.scenarioData.UseCustomData {
		storageConfig := map[string]any{
			"containers": []string{"blobs"},
		}

		storageConfigJson, err := json.Marshal(storageConfig)
		if err != nil {
			return fmt.Errorf("failed to marshal storage config: %w", err)
		}

		storageResource := &azdext.ComposedResource{
			Name:   "storage",
			Type:   "storage",
			Config: storageConfigJson,
		}

		resourcesToAdd = append(resourcesToAdd, storageResource)
	}

	models := []*ai.AiModelDeployment{}

	// Add AI model resources
	if len(a.scenarioData.ModelSelections) > 0 {
		aiProject := &azdext.ComposedResource{
			Name: "ai-project",
			Type: "ai.project",
		}

		for _, modelName := range a.scenarioData.ModelSelections {
			aiModel, exists := a.modelCatalog[modelName]
			if exists {
				modelDeployment, err := a.modelCatalogService.GetModelDeployment(ctx, aiModel, nil)
				if err != nil {
					return fmt.Errorf("failed to get model deployment: %w", err)
				}

				models = append(models, modelDeployment)
			}
		}

		resourceConfig := map[string]any{
			"models": models,
		}

		configJson, err := json.Marshal(resourceConfig)
		if err != nil {
			return fmt.Errorf("failed to marshal AI project config: %w", err)
		}

		aiProject.Config = configJson
		resourcesToAdd = append(resourcesToAdd, aiProject)
	}

	for _, service := range servicesToAdd {
		_, err := a.azdClient.Project().AddService(ctx, &azdext.AddServiceRequest{
			Service: service,
		})
		if err != nil {
			return fmt.Errorf("failed to add service %s: %w", service.Name, err)
		}

		// Copy files from the embedded resources to the local service path.
		servicePath := filepath.Join(a.projectConfig.Path, service.RelativePath)
		if err := os.MkdirAll(servicePath, os.ModePerm); err != nil {
			return fmt.Errorf("failed to create service path %s: %w", servicePath, err)
		}

		isEmpty, err := util.IsDirEmpty(servicePath)
		if err != nil {
			return fmt.Errorf("failed to check if directory is empty: %w", err)
		}

		if !isEmpty {
			if err := spinner.Stop(ctx); err != nil {
				return fmt.Errorf("failed to stop spinner: %w", err)
			}

			overwriteResponse, err := a.azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
				Options: &azdext.ConfirmOptions{
					DefaultValue: to.Ptr(false),
					Message: fmt.Sprintf(
						"The directory %s is not empty. Do you want to overwrite it?",
						output.WithHighLightFormat(service.RelativePath),
					),
				},
			})

			if err != nil {
				return fmt.Errorf("failed to confirm overwrite: %w", err)
			}

			if !*overwriteResponse.Value {
				continue
			}

			if err := spinner.Start(ctx); err != nil {
				return fmt.Errorf("failed to start spinner: %w", err)
			}
		}

		// Determine the correct resource folder path using POSIX join.
		resourceDir := path.Join("scenarios", a.scenarioData.SelectedScenario, service.Name, service.Language)
		entries, err := resources.Scenarios.ReadDir(resourceDir)
		if err != nil {
			return fmt.Errorf("failed to read resource directory %s: %w", resourceDir, err)
		}

		for _, entry := range entries {
			srcPath := path.Join(resourceDir, entry.Name())
			destPath := filepath.Join(servicePath, entry.Name())
			data, err := resources.Scenarios.ReadFile(srcPath)
			if err != nil {
				return fmt.Errorf("failed to read resource file %s: %w", srcPath, err)
			}
			//nolint:gosec
			if err := os.WriteFile(destPath, data, 0644); err != nil {
				return fmt.Errorf("failed to write file %s: %w", destPath, err)
			}
		}
	}

	for _, resource := range resourcesToAdd {
		_, err := a.azdClient.Compose().AddResource(ctx, &azdext.AddResourceRequest{
			Resource: resource,
		})
		if err != nil {
			return fmt.Errorf("failed to add resource %s: %w", resource.Name, err)
		}
	}

	if err := spinner.Stop(ctx); err != nil {
		return fmt.Errorf("failed to stop spinner: %w", err)
	}

	fmt.Println(output.WithSuccessFormat("SUCCESS! The following have been staged for provisioning and deployment:"))

	if len(servicesToAdd) > 0 {
		fmt.Println()
		fmt.Println(output.WithHintFormat("Services"))
		for _, service := range servicesToAdd {
			fmt.Printf("  - %s %s\n",
				service.Name,
				output.WithGrayFormat(
					"(Host: %s, Language: %s)",
					service.Host,
					service.Language,
				),
			)
		}
	}

	if len(resourcesToAdd) > 0 {
		fmt.Println()
		fmt.Println(output.WithHintFormat("Resources"))
		for _, resource := range resourcesToAdd {
			fmt.Printf("  - %s %s\n", resource.Name, output.WithGrayFormat("(%s)", resource.Type))
		}
	}

	if len(models) > 0 {
		fmt.Println()
		fmt.Println(output.WithHintFormat("AI Models"))
		for _, modelDeployment := range models {
			fmt.Printf("  - %s %s\n",
				modelDeployment.Name,
				output.WithGrayFormat(
					"(Format: %s, Version: %s, SKU: %s)",
					modelDeployment.Format,
					modelDeployment.Version,
					modelDeployment.Sku.Name,
				),
			)
		}
	}

	fmt.Println()
	confirmResponse, err := a.azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{
			Message:      "Do you want to provision resources to your project now?",
			DefaultValue: to.Ptr(true),
			HelpMessage:  "Provisioning resources will create the necessary Azure infrastructure for your application.",
		},
	})
	if err != nil {
		return fmt.Errorf("failed to confirm provisioning: %w", err)
	}

	if !*confirmResponse.Value {
		fmt.Println()
		fmt.Printf("To provision resources later, run %s\n", output.WithHighLightFormat("azd provision"))
		return nil
	}

	workflow := &azdext.Workflow{
		Name: "provision",
		Steps: []*azdext.WorkflowStep{
			{
				Command: &azdext.WorkflowCommand{
					Args: []string{"provision"},
				},
			},
		},
	}

	_, err = a.azdClient.Workflow().Run(ctx, &azdext.RunWorkflowRequest{
		Workflow: workflow,
	})

	if err != nil {
		return fmt.Errorf("failed to run provision workflow: %w", err)
	}

	fmt.Println()
	fmt.Println(output.WithSuccessFormat("SUCCESS! Your Azure resources have been provisioned."))
	fmt.Printf(
		"You can add additional resources to your project by running %s\n",
		output.WithHighLightFormat("azd compose add"),
	)

	return nil
}

func (a *startAction) loadAiCatalog(ctx context.Context) error {
	if a.modelCatalog != nil {
		return nil
	}

	spinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text:        "Loading AI Model Catalog",
		ClearOnStop: true,
	})

	if err := spinner.Start(ctx); err != nil {
		return fmt.Errorf("failed to start spinner: %w", err)
	}

	aiModelCatalog, err := a.modelCatalogService.ListAllModels(ctx, a.azureContext.Scope.SubscriptionId)
	if err != nil {
		return fmt.Errorf("failed to load AI model catalog: %w", err)
	}

	if err := spinner.Stop(ctx); err != nil {
		return err
	}

	a.modelCatalog = aiModelCatalog
	return nil
}

func ensureAzureContext(
	ctx context.Context,
	azdClient *azdext.AzdClient,
) (*azdext.AzureContext, *azdext.ProjectConfig, error) {
	getProjectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return nil, nil, fmt.Errorf("project not found. Run `azd init` to create a new project, %w", err)
	}

	envResponse, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return nil, nil, fmt.Errorf("environment not found. Run `azd env new` to create a new environment, %w", err)
	}

	envValues, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
		Name: envResponse.Environment.Name,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get environment values: %w", err)
	}

	envValueMap := make(map[string]string)
	for _, value := range envValues.KeyValues {
		envValueMap[value.Key] = value.Value
	}

	azureContext := &azdext.AzureContext{
		Scope: &azdext.AzureScope{
			TenantId:       envValueMap["AZURE_TENANT_ID"],
			SubscriptionId: envValueMap["AZURE_SUBSCRIPTION_ID"],
			Location:       envValueMap["AZURE_LOCATION"],
		},
		Resources: []string{},
	}

	if azureContext.Scope.SubscriptionId == "" {
		fmt.Print()
		fmt.Println("It looks like we first need to connect to your Azure subscription.")

		subscriptionResponse, err := azdClient.Prompt().PromptSubscription(ctx, &azdext.PromptSubscriptionRequest{})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to prompt for subscription: %w", err)
		}

		azureContext.Scope.SubscriptionId = subscriptionResponse.Subscription.Id
		azureContext.Scope.TenantId = subscriptionResponse.Subscription.TenantId

		// Set the subscription ID in the environment
		_, err = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: envResponse.Environment.Name,
			Key:     "AZURE_TENANT_ID",
			Value:   azureContext.Scope.TenantId,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to set tenant ID in environment: %w", err)
		}

		// Set the tenant ID in the environment
		_, err = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: envResponse.Environment.Name,
			Key:     "AZURE_SUBSCRIPTION_ID",
			Value:   azureContext.Scope.SubscriptionId,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to set subscription ID in environment: %w", err)
		}
	}

	if azureContext.Scope.Location == "" {
		fmt.Println()
		fmt.Println(
			"Next, we need to select a default Azure location that will be used as the target for your infrastructure.",
		)

		locationResponse, err := azdClient.Prompt().PromptLocation(ctx, &azdext.PromptLocationRequest{
			AzureContext: azureContext,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to prompt for location: %w", err)
		}

		azureContext.Scope.Location = locationResponse.Location.Name

		// Set the location in the environment
		_, err = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: envResponse.Environment.Name,
			Key:     "AZURE_LOCATION",
			Value:   azureContext.Scope.Location,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to set location in environment: %w", err)
		}
	}

	return azureContext, getProjectResponse.Project, nil
}

func (a *startAction) createQuestions() map[string]qna.Question {
	return map[string]qna.Question{
		"root": {
			Binding: &a.scenarioData.SelectedScenario,
			Heading: "Identify AI Scenario",
			Message: "Let's start drilling into your AI scenario to identify all the required infrastructure we will need.",
			Prompt: &qna.SingleSelectPrompt{
				Client:          a.azdClient,
				Message:         "What type of AI scenario are you building?",
				HelpMessage:     "Choose the scenario that best fits your needs.",
				EnableFiltering: to.Ptr(false),
				Choices: []qna.Choice{
					{Label: "RAG Application (Retrieval-Augmented Generation)", Value: "rag"},
					{Label: "AI Agent", Value: "agent"},
					{Label: "Other Scenarios (Coming Soon)", Value: "other-scenarios"},
				},
			},
			Branches: map[any]string{
				"rag":   "use-custom-data",
				"agent": "agent-tasks",
			},
		},
		"use-custom-data": {
			Binding: &a.scenarioData.UseCustomData,
			Prompt: &qna.ConfirmPrompt{
				Client:       a.azdClient,
				Message:      "Does your application require custom data?",
				HelpMessage:  "Custom data is data that is not publicly available and is specific to your application.",
				DefaultValue: to.Ptr(true),
			},
			AfterAsk: func(ctx context.Context, q *qna.Question, _ any) error {
				switch a.scenarioData.SelectedScenario {
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
		},
		"choose-data-types": {
			Binding: &a.scenarioData.DataTypes,
			Heading: "Data Sources",
			Message: "Lets identify all the data source that will be used in your application.",
			Prompt: &qna.MultiSelectPrompt{
				Client:          a.azdClient,
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
			Next: []qna.QuestionReference{{Key: "data-location"}},
		},
		"data-location": {
			Binding: &a.scenarioData.DataLocations,
			Prompt: &qna.MultiSelectPrompt{
				Client:          a.azdClient,
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
			Next: []qna.QuestionReference{{Key: "choose-vector-store-type"}},
		},
		"choose-storage": {
			Binding: &a.scenarioData.StorageAccountId,
			Heading: "Storage Account",
			Message: "We'll need to setup a storage account to store the data for your application.",
			Prompt: &qna.SubscriptionResourcePrompt{
				Client:                  a.azdClient,
				ResourceType:            "Microsoft.Storage/storageAccounts",
				ResourceTypeDisplayName: "Storage Account",
				HelpMessage:             "Select an existing storage account or create a new one.",
				AzureContext:            a.azureContext,
			},
		},
		"choose-database-type": {
			Binding: &a.scenarioData.DatabaseType,
			Heading: "Database",
			Message: "We'll need to setup a database that will be used by your application to power AI model(s).",
			Prompt: &qna.SingleSelectPrompt{
				Message:         "Which type of database?",
				HelpMessage:     "Select the type of database that best fits your needs.",
				Client:          a.azdClient,
				EnableFiltering: to.Ptr(false),
				Choices: []qna.Choice{
					{Label: "CosmosDB", Value: "db.cosmos"},
					{Label: "PostgreSQL", Value: "db.postgres"},
					{Label: "MySQL", Value: "db.mysql"},
					{Label: "Redis", Value: "db.redis"},
					{Label: "MongoDB", Value: "db.mongo"},
				},
			},
			Next: []qna.QuestionReference{{Key: "choose-database-resource"}},
		},
		"choose-database-resource": {
			Binding: &a.scenarioData.DatabaseId,
			Prompt: &qna.SubscriptionResourcePrompt{
				HelpMessage:  "Select an existing database or create a new one.",
				Client:       a.azdClient,
				AzureContext: a.azureContext,
				BeforeAsk: func(ctx context.Context, q *qna.Question, p *qna.SubscriptionResourcePrompt) error {
					resourceType, has := dbResourceMap[a.scenarioData.DatabaseType]
					if !has {
						return fmt.Errorf(
							"unknown resource type for database: %s",
							a.scenarioData.DatabaseType,
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
			Binding: &a.scenarioData.LocalFilePath,
			Heading: "Local File System",
			Message: "Lets identify the files that will be used in your application. " +
				"Later on we will upload these files to Azure so they can be used by your application.",
			Prompt: &qna.TextPrompt{
				Client:  a.azdClient,
				Message: "Path to the local files",
				HelpMessage: "This path can be absolute or relative to the current working directory. " +
					"Please make sure the path is accessible from the machine running this command.",
				Placeholder: "./data",
			},
			Next: []qna.QuestionReference{{Key: "local-file-choose-files"}},
		},
		"local-file-choose-files": {
			Binding: &a.scenarioData.LocalFileSelection,
			Prompt: &qna.SingleSelectPrompt{
				Client:          a.azdClient,
				Message:         "Which files?",
				HelpMessage:     "Select all files or use a glob expression to filter the files.",
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
			Binding: &a.scenarioData.LocalFileGlobFilter,
			Prompt: &qna.TextPrompt{
				Client:  a.azdClient,
				Message: "Enter a glob expression to filter files",
				HelpMessage: "A glob expression is a string that uses wildcard characters to match file names. " +
					" For example, *.txt will match all text files in the current directory.",
				Placeholder: "*.json",
			},
		},
		"choose-vector-store-type": {
			Binding: &a.scenarioData.VectorStoreType,
			Heading: "Vector Store",
			Message: "Based on your choices we're going to need a vector store to store the text embeddings for your data.",
			Prompt: &qna.SingleSelectPrompt{
				Message:         "What type of vector store do you want to use?",
				HelpMessage:     "Select the type of vector store that best fits your needs.",
				Client:          a.azdClient,
				EnableFiltering: to.Ptr(false),
				Choices: []qna.Choice{
					{Label: "Choose for me", Value: "choose-vector-store"},
					{Label: "AI Search", Value: "ai.search"},
					{Label: "CosmosDB", Value: "db.cosmos"},
				},
			},
			Branches: map[any]string{
				"ai.search": "choose-vector-store-resource",
				"db.cosmos": "choose-vector-store-resource",
			},
			AfterAsk: func(ctx context.Context, q *qna.Question, _ any) error {
				switch a.scenarioData.SelectedScenario {
				case "rag":
					q.Next = []qna.QuestionReference{{Key: "rag-user-interaction"}}
				case "agent":
					q.Next = []qna.QuestionReference{{Key: "agent-interaction"}}
				}
				return nil
			},
		},
		"choose-vector-store-resource": {
			Binding: &a.scenarioData.VectorStoreId,
			Prompt: &qna.SubscriptionResourcePrompt{
				HelpMessage:  "Select an existing vector store or create a new one.",
				Client:       a.azdClient,
				AzureContext: a.azureContext,
				BeforeAsk: func(ctx context.Context, q *qna.Question, p *qna.SubscriptionResourcePrompt) error {
					resourceType, has := vectorStoreMap[a.scenarioData.VectorStoreType]
					if !has {
						return fmt.Errorf(
							"unknown resource type for vector store: %s",
							a.scenarioData.VectorStoreType,
						)
					}

					p.ResourceType = resourceType.ResourceType
					p.Kinds = resourceType.Kinds
					p.ResourceTypeDisplayName = resourceType.ResourceTypeDisplayName

					return nil
				},
			},
		},
		"rag-user-interaction": {
			Binding: &a.scenarioData.InteractionTypes,
			Heading: "User Interaction",
			Message: "Now we will figure out all the different ways users will interact with your application.",
			Prompt: &qna.MultiSelectPrompt{
				Client:          a.azdClient,
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
				q.State["interactionTypes"] = value
				return nil
			},
			Next: []qna.QuestionReference{{Key: "start-choose-models"}},
		},
		"agent-interaction": {
			Binding: &a.scenarioData.InteractionTypes,
			Heading: "Agent Hosting",
			Message: "Now we will figure out all the different ways users and systems will interact with your agent.",
			Prompt: &qna.MultiSelectPrompt{
				Client:          a.azdClient,
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
				q.State["interactionTypes"] = value
				return nil
			},
			Next: []qna.QuestionReference{{Key: "start-choose-model"}},
		},
		"agent-tasks": {
			Binding: &a.scenarioData.ModelTasks,
			Prompt: &qna.MultiSelectPrompt{
				Client:          a.azdClient,
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
			Next: []qna.QuestionReference{{Key: "use-custom-data"}},
		},
		"choose-app-type": {
			Binding: &a.scenarioData.AppHostTypes,
			BeforeAsk: func(ctx context.Context, q *qna.Question, value any) error {
				q.Heading = fmt.Sprintf("Configure '%s' Application", value)
				q.Message = fmt.Sprintf("Lets collect some information about your %s application.", value)
				q.State["interactionType"] = value
				return nil
			},
			Prompt: &qna.SingleSelectPrompt{
				Message:         "Which application host do you want to use?",
				Client:          a.azdClient,
				EnableFiltering: to.Ptr(false),
				Choices: []qna.Choice{
					{Label: "Choose for me", Value: "choose-app"},
					{Label: "Container App", Value: "host.containerapp"},
					{Label: "App Service (Coming Soon)", Value: "host.webapp"},
					{Label: "Function App (Coming Soon)", Value: "host.functionapp"},
					{Label: "Static Web App (Coming Soon)", Value: "host.staticwebapp"},
					{Label: "Other", Value: "other-app"},
				},
			},
			Branches: map[any]string{
				"host.containerapp": "choose-app-resource",
			},
			Next: []qna.QuestionReference{
				{Key: "choose-app-language"},
			},
		},
		"choose-app-language": {
			Binding: &a.scenarioData.AppLanguages,
			Prompt: &qna.SingleSelectPrompt{
				Client:          a.azdClient,
				Message:         "Which programming language do you want to use?",
				HelpMessage:     "Select the programming language that best fits your needs.",
				EnableFiltering: to.Ptr(false),
				Choices: []qna.Choice{
					{Label: "Choose for me", Value: "python"},
					{Label: "C#", Value: "csharp"},
					{Label: "Python", Value: "python"},
					{Label: "JavaScript", Value: "js"},
					{Label: "TypeScript", Value: "ts"},
					{Label: "Java", Value: "java"},
					{Label: "Other", Value: "other"},
				},
			},
		},
		"choose-app-resource": {
			Binding: &a.scenarioData.AppResourceIds,
			BeforeAsk: func(ctx context.Context, q *qna.Question, value any) error {
				q.State["appType"] = value
				return nil
			},
			Prompt: &qna.SubscriptionResourcePrompt{
				HelpMessage:  "Select an existing application or create a new one.",
				Client:       a.azdClient,
				AzureContext: a.azureContext,
				BeforeAsk: func(ctx context.Context, q *qna.Question, p *qna.SubscriptionResourcePrompt) error {
					appType := q.State["appType"].(string)

					resourceType, has := appResourceMap[appType]
					if !has {
						return fmt.Errorf(
							"unknown resource type for database: %s",
							appType,
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
			Binding: &a.scenarioData.MessagingType,
			Prompt: &qna.SingleSelectPrompt{
				Client:          a.azdClient,
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
			Binding: &a.scenarioData.MessagingId,
			Prompt: &qna.SubscriptionResourcePrompt{
				HelpMessage:  "Select an existing messaging service or create a new one.",
				Client:       a.azdClient,
				AzureContext: a.azureContext,
				BeforeAsk: func(ctx context.Context, q *qna.Question, p *qna.SubscriptionResourcePrompt) error {
					resourceType, has := messagingResourceMap[a.scenarioData.MessagingType]
					if !has {
						return fmt.Errorf(
							"unknown resource type for database: %s",
							a.scenarioData.MessagingType,
						)
					}

					p.ResourceType = resourceType.ResourceType
					p.Kinds = resourceType.Kinds
					p.ResourceTypeDisplayName = resourceType.ResourceTypeDisplayName

					return nil
				},
			},
		},
		"start-choose-models": {
			Heading: "AI Model Selection",
			Message: "Now we will figure out the best AI model(s) for your application.",
			AfterAsk: func(ctx context.Context, question *qna.Question, value any) error {
				if err := a.loadAiCatalog(ctx); err != nil {
					return fmt.Errorf("failed to load AI model catalog: %w", err)
				}

				allModelTypes := map[string]struct {
					Heading           string
					Description       string
					QuestionReference qna.QuestionReference
				}{
					"llm": {
						Heading:     "Large Language Model (LLM) (For generating responses)",
						Description: "Processes user queries and retrieved documents to generate intelligent responses.",
						QuestionReference: qna.QuestionReference{
							Key: "start-choose-model",
							State: map[string]any{
								"modelSelectMessage": "Lets choose a chat completion model",
								"capabilities":       []string{"chatCompletion"},
							},
						},
					},
					"embedding": {
						Heading: "Embedding Model (For vectorizing text)",
						Description: "Used to convert documents and queries into vector representations " +
							"for efficient similarity searches.",
						QuestionReference: qna.QuestionReference{
							Key: "start-choose-model",
							State: map[string]any{
								"modelSelectMessage": "Lets choose a text embedding model",
								"capabilities":       []string{"embeddings"},
							},
						},
					},
				}

				requiredModels := []string{"llm"}
				if a.scenarioData.UseCustomData {
					requiredModels = append(requiredModels, "embedding")
				}

				nextQuestions := []qna.QuestionReference{}

				fmt.Printf("  Based on your choices, you will need the following AI models:\n\n")
				for _, model := range requiredModels {
					if modelType, ok := allModelTypes[model]; ok {
						//nolint printf
						fmt.Printf("  - %s\n", output.WithBold(modelType.Heading))
						fmt.Printf("    %s\n", output.WithGrayFormat(modelType.Description))
						fmt.Println()
						nextQuestions = append(nextQuestions, modelType.QuestionReference)
					}
				}

				question.Next = nextQuestions

				return nil
			},
		},
		"start-choose-model": {
			BeforeAsk: func(ctx context.Context, question *qna.Question, value any) error {
				if err := a.loadAiCatalog(ctx); err != nil {
					return fmt.Errorf("failed to load AI model catalog: %w", err)
				}

				return nil
			},
			Prompt: &qna.SingleSelectPrompt{
				Client: a.azdClient,
				BeforeAsk: func(ctx context.Context, q *qna.Question, p *qna.SingleSelectPrompt) error {
					// Override the message
					if message, ok := q.State["modelSelectMessage"].(string); ok {
						p.Message = message
					}

					return nil
				},
				Message:         "How do you want to find the right model?",
				HelpMessage:     "Select the option that best fits your needs.",
				EnableFiltering: to.Ptr(false),
				Choices: []qna.Choice{
					{Label: "Choose for me", Value: "choose-model"},
					{Label: "Help me choose", Value: "guide-model"},
					{Label: "I will choose model", Value: "user-model"},
				},
			},
			AfterAsk: func(ctx context.Context, q *qna.Question, value any) error {
				selectedValue := value.(string)
				if selectedValue == "choose-model" {
					capabilities, ok := q.State["capabilities"].([]string)
					if !ok {
						return nil
					}

					// If the user selected "choose-model", we need to set the model selection
					if slices.Contains(capabilities, "chatCompletion") {
						a.scenarioData.ModelSelections = append(
							a.scenarioData.ModelSelections,
							defaultChatCompletionModel,
						)
					} else if slices.Contains(capabilities, "embeddings") {
						a.scenarioData.ModelSelections = append(
							a.scenarioData.ModelSelections,
							defaultEmbeddingModel,
						)
					}
				}

				return nil
			},
			Branches: map[any]string{
				"guide-model": "guide-model-select",
				"user-model":  "user-model-select",
			},
		},
		"user-model-select": {
			Binding: &a.scenarioData.ModelSelections,
			Prompt: &qna.SingleSelectPrompt{
				BeforeAsk: func(ctx context.Context, q *qna.Question, p *qna.SingleSelectPrompt) error {
					var capabilities []string
					var formats []string
					var statuses []string
					var locations []string

					if val, ok := q.State["capabilities"]; ok {
						capabilities = val.([]string)
					}
					if val, ok := q.State["formats"]; ok {
						formats = val.([]string)
					}
					if val, ok := q.State["status"]; ok {
						statuses = val.([]string)
					}
					if val, ok := q.State["locations"]; ok {
						locations = val.([]string)
					}

					filterOptions := &ai.FilterOptions{
						Capabilities: capabilities,
						Formats:      formats,
						Statuses:     statuses,
						Locations:    locations,
					}
					filteredModels := a.modelCatalogService.ListFilteredModels(ctx, a.modelCatalog, filterOptions)

					choices := make([]qna.Choice, len(filteredModels))
					for i, model := range filteredModels {
						choices[i] = qna.Choice{
							Label: fmt.Sprintf("%s %s",
								model.Name,
								output.WithGrayFormat("(%s)", *model.Locations[0].Model.Model.Format),
							),
							Value: model.Name,
						}
					}
					p.Choices = choices

					return nil
				},
				Client:      a.azdClient,
				Message:     "Which model do you want to use?",
				HelpMessage: "Select the model that best fits your needs.",
			},
			AfterAsk: func(ctx context.Context, q *qna.Question, value any) error {
				delete(q.State, "capabilities")
				delete(q.State, "formats")
				delete(q.State, "status")
				delete(q.State, "locations")
				return nil
			},
		},
		"guide-model-select": {
			Prompt: &qna.MultiSelectPrompt{
				Client:  a.azdClient,
				Message: "Filter AI Models",
				HelpMessage: "Select all the filters that apply to your application. " +
					"These filters will help you narrow down the type of models you need.",
				EnableFiltering: to.Ptr(false),
				BeforeAsk: func(ctx context.Context, q *qna.Question, p *qna.MultiSelectPrompt) error {
					choices := []qna.Choice{}

					if _, has := q.State["capabilities"]; !has {
						choices = append(choices, qna.Choice{
							Label: "Filter by capabilities",
							Value: "filter-model-capability",
						})
					}

					if _, has := q.State["formats"]; !has {
						choices = append(choices, qna.Choice{
							Label: "Filter by author",
							Value: "filter-model-format",
						})
					}
					if _, has := q.State["status"]; !has {
						choices = append(choices, qna.Choice{
							Label: "Filter by status",
							Value: "filter-model-status",
						})
					}
					if _, has := q.State["locations"]; !has {
						choices = append(choices, qna.Choice{
							Label: "Filter by location",
							Value: "filter-model-location",
						})
					}

					p.Choices = choices
					return nil
				},
			},
			Branches: map[any]string{
				"filter-model-capability": "filter-model-capability",
				"filter-model-format":     "filter-model-format",
				"filter-model-status":     "filter-model-status",
				"filter-model-location":   "filter-model-location",
			},
			Next: []qna.QuestionReference{{Key: "user-model-select"}},
		},
		"filter-model-capability": {
			Prompt: &qna.MultiSelectPrompt{
				Client:      a.azdClient,
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
				q.State["capabilities"] = value
				return nil
			},
		},
		"filter-model-format": {
			Prompt: &qna.MultiSelectPrompt{
				BeforeAsk: func(ctx context.Context, q *qna.Question, p *qna.MultiSelectPrompt) error {
					formats := a.modelCatalogService.ListAllFormats(ctx, a.modelCatalog)
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
				Client:      a.azdClient,
				Message:     "Filter my by company or creator",
				HelpMessage: "Select all the companies or creators that apply to your application.",
			},
			AfterAsk: func(ctx context.Context, q *qna.Question, value any) error {
				q.State["formats"] = value
				return nil
			},
		},
		"filter-model-status": {
			Prompt: &qna.MultiSelectPrompt{
				BeforeAsk: func(ctx context.Context, q *qna.Question, p *qna.MultiSelectPrompt) error {
					statuses := a.modelCatalogService.ListAllStatuses(ctx, a.modelCatalog)
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
				Client:          a.azdClient,
				Message:         "Filter by model release status?",
				HelpMessage:     "Select all the model release status that apply to your application.",
				EnableFiltering: to.Ptr(false),
			},
			AfterAsk: func(ctx context.Context, q *qna.Question, value any) error {
				q.State["status"] = value
				return nil
			},
		},
		"filter-model-location": {
			Prompt: &qna.MultiSelectPrompt{
				BeforeAsk: func(ctx context.Context, q *qna.Question, p *qna.MultiSelectPrompt) error {
					spinner := ux.NewSpinner(&ux.SpinnerOptions{
						Text: "Loading Locations",
					})

					err := spinner.Run(ctx, func(ctx context.Context) error {
						locations, err := a.azureClient.ListLocations(ctx, a.azureContext.Scope.SubscriptionId)
						if err != nil {
							return fmt.Errorf("failed to list locations: %w", err)
						}

						choices := make([]qna.Choice, len(locations))
						for i, location := range locations {
							choices[i] = qna.Choice{
								Label: fmt.Sprintf("%s (%s)", *location.DisplayName, *location.Name),
								Value: *location.Name,
							}
						}

						p.Choices = choices
						return nil
					})
					if err != nil {
						return fmt.Errorf("failed to load locations: %w", err)
					}

					return nil
				},
				Client:      a.azdClient,
				Message:     "Filter by model location?",
				HelpMessage: "Select all the model locations that apply to your application.",
			},
			AfterAsk: func(ctx context.Context, q *qna.Question, value any) error {
				q.State["locations"] = value
				return nil
			},
		},
	}
}
