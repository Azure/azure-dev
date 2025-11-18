// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/spf13/cobra"
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

type appServiceRuntime struct {
	Stack   string `json:"stack,omitempty"`
	Version string `json:"version,omitempty"`
}

var (
	appServiceStackMap = map[string]appServiceRuntime{
		"python": {
			Stack:   "python",
			Version: "3.13",
		},
		"js": {
			Stack:   "node",
			Version: "22-lts",
		},
		"ts": {
			Stack:   "node",
			Version: "22-lts",
		},
	}

	defaultModelMap = map[string]string{
		"chatCompletion":   "gpt-4o",
		"embeddings":       "text-embedding-3-small",
		"imageGenerations": "dall-e-3",
		"audio":            "whisper",
	}

	defaultAppLanguageMap = map[string]string{
		"rag-ui":          "ts",
		"rag-api":         "python",
		"agent-ui":        "ts",
		"agent-api":       "python",
		"agent-messaging": "python",
	}

	appUsesMap = map[string][]string{
		"rag-ui": {
			"host.containerapp",
		},
		"rag-api": {
			"ai.project",
			"ai.search",
			"db.cosmos",
			"db.postgres",
			"db.redis",
			"db.mongo",
			"db.mysql",
			"messaging.eventhubs",
			"messaging.servicebus",
			"storage",
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

			getComposedResourcesResponse, err := azdClient.Compose().ListResources(ctx, &azdext.EmptyRequest{})
			if err != nil {
				return fmt.Errorf("failed to get composed resources: %w", err)
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
				composedResources:   getComposedResourcesResponse.Resources,
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
	composedResources   []*azdext.ComposedResource
}

func (a *startAction) Run(ctx context.Context, args []string) error {
	// Build up list of questions
	listOfQuestions, err := a.createQuestions(ctx)
	if err != nil {
		return fmt.Errorf("failed to generate prompt: %w", err)
	}

	decisionTree := qna.NewDecisionTree(listOfQuestions)
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

	resourcesToAdd := map[string]*azdext.ComposedResource{}
	servicesToAdd := map[string]*azdext.ServiceConfig{}

	// Add database resources
	if a.scenarioData.DatabaseType != "" {
		desiredName := strings.ReplaceAll(a.scenarioData.DatabaseType, "db.", "")
		dbResource := &azdext.ComposedResource{
			Name:       a.generateResourceName(desiredName),
			Type:       a.scenarioData.DatabaseType,
			ResourceId: a.scenarioData.DatabaseId,
		}
		resourcesToAdd[dbResource.Name] = dbResource
	}

	// Add messaging resources
	if a.scenarioData.MessagingType != "" {
		desiredName := strings.ReplaceAll(a.scenarioData.MessagingType, "messaging.", "")
		messagingResource := &azdext.ComposedResource{
			Name:       a.generateResourceName(desiredName),
			Type:       a.scenarioData.MessagingType,
			ResourceId: a.scenarioData.MessagingId,
		}
		resourcesToAdd[messagingResource.Name] = messagingResource
	}

	// Add vector store resources
	if a.scenarioData.VectorStoreType != "" {
		vectorStoreResource := &azdext.ComposedResource{
			Name:       a.generateResourceName("vector-store"),
			Type:       a.scenarioData.VectorStoreType,
			ResourceId: a.scenarioData.VectorStoreId,
		}
		resourcesToAdd[vectorStoreResource.Name] = vectorStoreResource
	}

	// Add storage resources
	if a.scenarioData.UseCustomData && a.scenarioData.StorageAccountId != "" {
		storageConfig := map[string]any{
			"containers": []string{
				"data",
				"embeddings",
			},
		}

		storageConfigJson, err := json.Marshal(storageConfig)
		if err != nil {
			return fmt.Errorf("failed to marshal storage config: %w", err)
		}

		storageResource := &azdext.ComposedResource{
			Name:       a.generateResourceName("storage"),
			Type:       "storage",
			Config:     storageConfigJson,
			ResourceId: a.scenarioData.StorageAccountId,
		}

		resourcesToAdd[storageResource.Name] = storageResource
	}

	models := []*ai.AiModelDeployment{}

	type AiProjectResourceConfig struct {
		Models []*ai.AiModelDeployment `json:"models,omitempty"`
	}

	// Add AI model resources
	if len(a.scenarioData.ModelSelections) > 0 {
		var aiProject *azdext.ComposedResource
		var aiProjectConfig *AiProjectResourceConfig
		for _, resource := range a.composedResources {
			if resource.Type == "ai.project" {
				aiProject = resource

				if err := json.Unmarshal(resource.Config, &aiProjectConfig); err != nil {
					return fmt.Errorf("failed to unmarshal AI project config: %w", err)
				}

				break
			}
		}

		if aiProject == nil {
			aiProject = &azdext.ComposedResource{
				Name: a.generateResourceName("ai-project"),
				Type: "ai.project",
			}
			aiProjectConfig = &AiProjectResourceConfig{}
		}

		modelMap := map[string]*ai.AiModelDeployment{}
		for _, modelDeployment := range aiProjectConfig.Models {
			modelMap[modelDeployment.Name] = modelDeployment
		}

		for _, modelName := range a.scenarioData.ModelSelections {
			aiModel, exists := a.modelCatalog[modelName]
			if exists {
				modelDeployment, err := a.modelCatalogService.GetModelDeployment(ctx, aiModel, nil)
				if err != nil {
					return fmt.Errorf("failed to get model deployment: %w", err)
				}

				if _, has := modelMap[modelDeployment.Name]; !has {
					modelMap[modelDeployment.Name] = modelDeployment
					aiProjectConfig.Models = append(aiProjectConfig.Models, modelDeployment)
					models = append(models, modelDeployment)
				}
			}
		}

		configJson, err := json.Marshal(aiProjectConfig)
		if err != nil {
			return fmt.Errorf("failed to marshal AI project config: %w", err)
		}

		aiProject.Config = configJson
		resourcesToAdd[aiProject.Name] = aiProject
	}

	// Add host resources such as container apps.
	for i, appKey := range a.scenarioData.InteractionTypes {
		if i >= len(a.scenarioData.AppHostTypes) {
			break
		}

		appType := a.scenarioData.AppHostTypes[i]
		if appType == "" || appType == "choose-app" {
			appType = "host.containerapp"
		}

		languageType := a.scenarioData.AppLanguages[i]

		appConfig := map[string]any{
			"port": 8080,
		}

		if appType == "host.appservice" {
			runtime, ok := appServiceStackMap[languageType]
			if !ok {
				return fmt.Errorf("unsupported language type: %s", languageType)
			}
			appConfig["runtime"] = runtime
		}

		appConfigJson, err := json.Marshal(appConfig)
		if err != nil {
			return fmt.Errorf("failed to marshal app config: %w", err)
		}

		resourceId := ""
		if a.scenarioData.AppResourceIds[i] != "new" {
			resourceId = a.scenarioData.AppResourceIds[i]
		}
		appResource := &azdext.ComposedResource{
			Name:       a.generateResourceName(appKey),
			Type:       appType,
			Config:     appConfigJson,
			Uses:       []string{},
			ResourceId: resourceId,
		}

		serviceName := a.generateServiceName(appKey)

		serviceConfig := &azdext.ServiceConfig{
			Name:         serviceName,
			Language:     languageType,
			Host:         strings.ReplaceAll(appType, "host.", ""),
			RelativePath: filepath.Join("src", serviceName),
		}

		// Setting the key of the service to the scenario interaction type since this is used for the
		// file copying.
		servicesToAdd[appKey] = serviceConfig
		resourcesToAdd[appResource.Name] = appResource
	}

	// Adds any new services to the azure.yaml.
	for interactionName, service := range servicesToAdd {
		_, err := a.azdClient.Project().AddService(ctx, &azdext.AddServiceRequest{
			Service: service,
		})
		if err != nil {
			return fmt.Errorf("failed to add service %s: %w", service.Name, err)
		}

		// Copy files from the embedded resources to the local service path.
		destServicePath := filepath.Join(a.projectConfig.Path, service.RelativePath)
		if err := os.MkdirAll(destServicePath, os.ModePerm); err != nil {
			return fmt.Errorf("failed to create service path %s: %w", destServicePath, err)
		}

		if !util.IsDirEmpty(destServicePath) {
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

		// Identify dependent resources.
		uses := appUsesMap[interactionName]
		resource := resourcesToAdd[service.Name]
		resourceUseMap := map[string]struct{}{}
		if len(uses) > 0 {
			for _, dependentResource := range resourcesToAdd {
				// Skip if the resource type is already added.
				if _, has := resourceUseMap[dependentResource.Type]; has {
					continue
				}

				if slices.Contains(uses, dependentResource.Type) && resource.Name != dependentResource.Name {
					resource.Uses = append(resource.Uses, dependentResource.Name)
					resourceUseMap[dependentResource.Type] = struct{}{}
				}
			}
			// Existing resources that are already in azure.yaml
			for _, existingResource := range a.composedResources {
				// Skip if the resource type is already added.
				if _, has := resourceUseMap[existingResource.Type]; has {
					continue
				}

				if slices.Contains(uses, existingResource.Type) && resource.Name != existingResource.Name {
					resource.Uses = append(resource.Uses, existingResource.Name)
					resourceUseMap[existingResource.Type] = struct{}{}
				}
			}
		}
	}

	// Add any new resources to the azure.yaml.
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

func ensureProject(ctx context.Context, azdClient *azdext.AzdClient) (*azdext.ProjectConfig, error) {
	projectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		fmt.Println("Lets get your project initialized.")

		// We don't have a project yet
		// Dispatch a workflow to init the project and create a new environment
		workflow := &azdext.Workflow{
			Name: "init",
			Steps: []*azdext.WorkflowStep{
				{Command: &azdext.WorkflowCommand{Args: []string{"init"}}},
			},
		}

		_, err := azdClient.Workflow().Run(ctx, &azdext.RunWorkflowRequest{
			Workflow: workflow,
		})

		if err != nil {
			return nil, fmt.Errorf("failed to initialize project: %w", err)
		}

		projectResponse, err = azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
		if err != nil {
			return nil, fmt.Errorf("failed to get project: %w", err)
		}

		fmt.Println()
	}

	if projectResponse.Project == nil {
		return nil, fmt.Errorf("project not found")
	}

	return projectResponse.Project, nil
}

func ensureEnvironment(ctx context.Context, azdClient *azdext.AzdClient) (*azdext.Environment, error) {
	envResponse, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		fmt.Println("Lets create a new default environment for your project.")

		// We don't have a project yet
		// Dispatch a workflow to init the project and create a new environment
		workflow := &azdext.Workflow{
			Name: "env new",
			Steps: []*azdext.WorkflowStep{
				{Command: &azdext.WorkflowCommand{Args: []string{"env", "new"}}},
			},
		}

		_, err = azdClient.Workflow().Run(ctx, &azdext.RunWorkflowRequest{
			Workflow: workflow,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create new environment: %w", err)
		}

		envResponse, err = azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
		if err != nil {
			return nil, fmt.Errorf("failed to get current environment: %w", err)
		}

		fmt.Println()
	}

	if envResponse.Environment == nil {
		return nil, fmt.Errorf("environment not found")
	}

	return envResponse.Environment, nil
}

func ensureAzureContext(
	ctx context.Context,
	azdClient *azdext.AzdClient,
) (*azdext.AzureContext, *azdext.ProjectConfig, error) {
	project, err := ensureProject(ctx, azdClient)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to ensure environment: %w", err)
	}

	env, err := ensureEnvironment(ctx, azdClient)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to ensure environment: %w", err)
	}

	envValues, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
		Name: env.Name,
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
			EnvName: env.Name,
			Key:     "AZURE_TENANT_ID",
			Value:   azureContext.Scope.TenantId,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to set tenant ID in environment: %w", err)
		}

		// Set the tenant ID in the environment
		_, err = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: env.Name,
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
			EnvName: env.Name,
			Key:     "AZURE_LOCATION",
			Value:   azureContext.Scope.Location,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to set location in environment: %w", err)
		}
	}

	return azureContext, project, nil
}

func (a *startAction) createQuestions(ctx context.Context) (map[string]qna.Question, error) {
	resourceTypes, err := a.azdClient.Compose().ListResourceTypes(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list resource types: %w", err)
	}

	dbResourceMap := make(map[string]*azdext.ComposedResourceType)
	vectorStoreMap := make(map[string]*azdext.ComposedResourceType)
	messagingResourceMap := make(map[string]*azdext.ComposedResourceType)
	appResourceMap := make(map[string]*azdext.ComposedResourceType)

	for _, resourceType := range resourceTypes.ResourceTypes {
		key := resourceType.Name
		if strings.HasPrefix(key, "db.") {
			dbResourceMap[key] = resourceType
		} else if strings.HasPrefix(key, "messaging.") {
			messagingResourceMap[key] = resourceType
		} else if strings.HasPrefix(key, "host.") {
			appResourceMap[key] = resourceType
		}

		if strings.Contains(key, "ai.search") || strings.Contains(key, "db.cosmos") {
			vectorStoreMap[key] = resourceType
		}
	}

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
			Branches: map[any][]qna.QuestionReference{
				"rag":   {{Key: "use-custom-data"}},
				"agent": {{Key: "agent-tasks"}},
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
					q.Branches = map[any][]qna.QuestionReference{
						true:  {{Key: "choose-data-types"}},
						false: {{Key: "rag-user-interaction"}},
					}
				case "agent":
					q.Branches = map[any][]qna.QuestionReference{
						true:  {{Key: "choose-data-types"}},
						false: {{Key: "agent-tasks"}},
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
			Branches: map[any][]qna.QuestionReference{
				"blob-storage":      {{Key: "choose-storage"}},
				"databases":         {{Key: "choose-database"}},
				"local-file-system": {{Key: "local-file-system"}},
			},
			Next: []qna.QuestionReference{{Key: "choose-vector-store"}},
		},
		"choose-storage": {
			Heading: "Storage Account",
			Message: "We'll need to setup a storage account to store the data for your application.",
			BeforeAsk: func(ctx context.Context, q *qna.Question, _ any) error {
				hasStorageResource := false
				for _, resource := range a.composedResources {
					if resource.Type == "storage" {
						hasStorageResource = true
						break
					}
				}

				promptMessage := "It looks like you already have a configured storage account. Do you want to reuse it?"

				if hasStorageResource {
					q.Prompt = &qna.ConfirmPrompt{
						Client:       a.azdClient,
						Message:      promptMessage,
						DefaultValue: to.Ptr(true),
						HelpMessage:  "Using an existing storage account will save you time and resources.",
					}
				}

				q.State["hasStorageResource"] = hasStorageResource

				return nil
			},
			AfterAsk: func(ctx context.Context, q *qna.Question, value any) error {
				hasStorageResource := q.State["hasStorageResource"].(bool)
				reuseStorage, ok := value.(bool)

				if !hasStorageResource || ok && !reuseStorage {
					q.Next = []qna.QuestionReference{{Key: "choose-storage-resource"}}
				}

				return nil
			},
		},
		"choose-storage-resource": {
			Binding: &a.scenarioData.StorageAccountId,
			Prompt: &qna.SubscriptionResourcePrompt{
				Client:                  a.azdClient,
				ResourceType:            "Microsoft.Storage/storageAccounts",
				ResourceTypeDisplayName: "Storage Account",
				HelpMessage:             "Select an existing storage account or create a new one.",
				AzureContext:            a.azureContext,
			},
		},
		"choose-database": {
			Heading: "Database",
			Message: "We'll need to setup a database that will be used by your application to power AI model(s).",
			BeforeAsk: func(ctx context.Context, q *qna.Question, _ any) error {
				hasDatabaseResource := false
				for _, resource := range a.composedResources {
					if strings.HasPrefix(resource.Type, "db.") {
						hasDatabaseResource = true
						break
					}
				}

				if hasDatabaseResource {
					q.Prompt = &qna.ConfirmPrompt{
						Client:       a.azdClient,
						Message:      "It looks like you already have a configured database. Do you want to reuse it?",
						DefaultValue: to.Ptr(true),
						HelpMessage:  "Using an existing database will save you time and resources.",
					}
				}

				q.State["hasDatabaseResource"] = hasDatabaseResource

				return nil
			},
			AfterAsk: func(ctx context.Context, q *qna.Question, value any) error {
				hasDatabaseResource := q.State["hasDatabaseResource"].(bool)
				reuseDatabase, ok := value.(bool)

				if !hasDatabaseResource || ok && !reuseDatabase {
					q.Next = []qna.QuestionReference{{Key: "choose-database-type"}}
				}

				return nil
			},
		},
		"choose-database-type": {
			Binding: &a.scenarioData.DatabaseType,
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

					p.ResourceType = resourceType.Type
					p.Kinds = resourceType.Kinds
					p.ResourceTypeDisplayName = resourceType.DisplayName

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
			Branches: map[any][]qna.QuestionReference{
				"glob-expression": {{Key: "local-file-glob"}},
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
		"choose-vector-store": {
			Heading: "Vector Store",
			Message: "Based on your choices we're going to need a vector store to store the text embeddings for your data.",
			BeforeAsk: func(ctx context.Context, q *qna.Question, _ any) error {
				hasVectorStoreResource := false
				for _, resource := range a.composedResources {
					if resource.Type == "ai.search" {
						hasVectorStoreResource = true
						break
					}
				}

				if hasVectorStoreResource {
					q.Prompt = &qna.ConfirmPrompt{
						Client:       a.azdClient,
						Message:      "It looks like you already have a configured vector store. Do you want to reuse it?",
						DefaultValue: to.Ptr(true),
						HelpMessage:  "Using an existing vector store will save you time and resources.",
					}
				}

				q.State["hasVectorStoreResource"] = hasVectorStoreResource

				return nil
			},
			AfterAsk: func(ctx context.Context, q *qna.Question, value any) error {
				hasVectorStoreResource := q.State["hasVectorStoreResource"].(bool)
				reuseVectorStore, ok := value.(bool)

				var next string
				if a.scenarioData.SelectedScenario == "rag" {
					next = "rag-user-interaction"
				} else {
					next = "agent-interaction"
				}

				if !hasVectorStoreResource || ok && !reuseVectorStore {
					next = "choose-vector-store-type"
				}

				q.Next = []qna.QuestionReference{{Key: next}}

				return nil
			},
		},
		"choose-vector-store-type": {
			Binding: &a.scenarioData.VectorStoreType,
			Prompt: &qna.SingleSelectPrompt{
				Message:         "What type of vector store do you want to use?",
				HelpMessage:     "Select the type of vector store that best fits your needs.",
				Client:          a.azdClient,
				EnableFiltering: to.Ptr(false),
				Choices: []qna.Choice{
					{Label: "Choose for me", Value: "ai.search"},
					{Label: "AI Search", Value: "ai.search"},
					{Label: "CosmosDB", Value: "db.cosmos"},
				},
			},
			Branches: map[any][]qna.QuestionReference{
				"ai.search": {{Key: "choose-vector-store-resource"}},
				"db.cosmos": {{Key: "choose-vector-store-resource"}},
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

					p.ResourceType = resourceType.Type
					p.Kinds = resourceType.Kinds
					p.ResourceTypeDisplayName = resourceType.DisplayName
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
					{Label: "Chatbot UI Frontend", Value: "rag-ui"},
					{Label: "API Backend Application", Value: "rag-api"},
				},
			},
			Branches: map[any][]qna.QuestionReference{
				"rag-ui":  {{Key: "choose-app"}},
				"rag-api": {{Key: "choose-app"}},
			},
			AfterAsk: func(ctx context.Context, q *qna.Question, value any) error {
				q.State["interactionTypes"] = value
				return nil
			},
			Next: []qna.QuestionReference{{Key: "start-choose-models"}},
		},
		"choose-app": {
			BeforeAsk: func(ctx context.Context, q *qna.Question, value any) error {
				q.Heading = fmt.Sprintf("Configure '%s' Application", value)
				q.Message = fmt.Sprintf("Lets collect some information about your %s application.", value)
				q.State["interactionType"] = value

				hasHostAppResource := false
				appHostCount := 0
				for _, resource := range a.composedResources {
					if strings.HasPrefix(resource.Type, "host.") {
						appHostCount++
						hasHostAppResource = true
					}
				}

				hostName := "host"
				hostName2 := "it"
				if appHostCount > 1 {
					hostName = "hosts"
					hostName2 = "them"
				}

				msg := fmt.Sprintf(
					"It looks like you project already contains %d application %s. Do you want to reuse %s?",
					appHostCount,
					hostName,
					hostName2,
				)

				if hasHostAppResource {
					q.Prompt = &qna.ConfirmPrompt{
						Client:       a.azdClient,
						Message:      msg,
						DefaultValue: to.Ptr(true),
						HelpMessage:  "Using an existing application host will save you time and resources.",
					}
				}

				q.State["hasHostAppResource"] = hasHostAppResource

				return nil
			},
			AfterAsk: func(ctx context.Context, q *qna.Question, value any) error {
				hasHostAppResource := q.State["hasHostAppResource"].(bool)
				reuseHostApp, ok := value.(bool)
				if !hasHostAppResource || ok && !reuseHostApp {
					q.Next = []qna.QuestionReference{{Key: "choose-app-type"}}
				}

				delete(q.State, "hasHostAppResource")

				return nil
			},
		},
		"choose-app-type": {
			Binding: &a.scenarioData.AppHostTypes,
			Prompt: &qna.SingleSelectPrompt{
				Message:         "Which application host do you want to use?",
				Client:          a.azdClient,
				EnableFiltering: to.Ptr(false),
				Choices: []qna.Choice{
					{Label: "Choose for me", Value: "choose-app"},
					{Label: "Container App", Value: "host.containerapp"},
					{Label: "App Service", Value: "host.appservice"},
					{Label: "Function App (Coming Soon)", Value: "host.functionapp"},
					{Label: "Static Web App (Coming Soon)", Value: "host.staticwebapp"},
					{Label: "Other", Value: "other-app"},
				},
			},
			Branches: map[any][]qna.QuestionReference{
				"host.containerapp": {{Key: "choose-app-resource"}},
				"host.appservice":   {{Key: "choose-app-resource"}},
			},
			Next: []qna.QuestionReference{
				{Key: "choose-app-language"},
			},
		},
		"choose-app-language": {
			Prompt: &qna.SingleSelectPrompt{
				Client:          a.azdClient,
				Message:         "Which programming language do you want to use?",
				HelpMessage:     "Select the programming language that best fits your needs.",
				EnableFiltering: to.Ptr(false),
				Choices: []qna.Choice{
					{Label: "Choose for me", Value: "default"},
					{Label: "C#", Value: "csharp"},
					{Label: "Python", Value: "python"},
					{Label: "JavaScript", Value: "js"},
					{Label: "TypeScript", Value: "ts"},
					{Label: "Java", Value: "java"},
					{Label: "Other", Value: "other"},
				},
			},
			AfterAsk: func(ctx context.Context, q *qna.Question, value any) error {
				selectedLanguage := value.(string)
				// Find the default language for the selected interaction type if available.
				if selectedLanguage == "default" {
					interactionType := q.State["interactionType"].(string)
					interactionDefault, has := defaultAppLanguageMap[interactionType]
					if has {
						selectedLanguage = interactionDefault
					} else {
						selectedLanguage = "python"
					}
				}

				// App Service in composability supports a limited set of runtimes, so re-prompt
				// if the user selects an unsupported language
				isAppService := len(a.scenarioData.AppHostTypes) > 0 &&
					a.scenarioData.AppHostTypes[len(a.scenarioData.AppHostTypes)-1] == "host.appservice"
				if isAppService {
					if _, ok := appServiceStackMap[selectedLanguage]; !ok {
						q.Branches = map[any][]qna.QuestionReference{
							selectedLanguage: {{Key: "choose-app-language"}},
						}
						fmt.Println(output.WithErrorFormat(
							"%s is not a supported language for App Service. Please choose another language.",
							selectedLanguage))
						return nil
					}
				}

				a.scenarioData.AppLanguages = append(a.scenarioData.AppLanguages, selectedLanguage)
				return nil
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

					p.ResourceType = resourceType.Type
					p.Kinds = resourceType.Kinds
					p.ResourceTypeDisplayName = resourceType.DisplayName

					return nil
				},
			},
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
					{Label: "Chatbot UI Frontend", Value: "agent-ui"},
					{Label: "API Backend Application", Value: "agent-api"},
					{Label: "Message based Backed Queue", Value: "agent-messaging"},
				},
			},
			Branches: map[any][]qna.QuestionReference{
				"agent-ui":        {{Key: "choose-app"}},
				"agent-api":       {{Key: "choose-app"}},
				"agent-messaging": {{Key: "choose-app"}, {Key: "choose-messaging"}},
			},
			AfterAsk: func(ctx context.Context, q *qna.Question, value any) error {
				q.State["interactionTypes"] = value
				return nil
			},
			Next: []qna.QuestionReference{{Key: "start-choose-models"}},
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
		"choose-messaging": {
			BeforeAsk: func(ctx context.Context, q *qna.Question, _ any) error {
				hasMessagingResource := false
				for _, resource := range a.composedResources {
					if strings.HasPrefix(resource.Type, "messaging.") {
						hasMessagingResource = true
						break
					}
				}

				if hasMessagingResource {
					promptMessage := "It looks like you already have a configured messaging source. Do you want to reuse it?"

					q.Prompt = &qna.ConfirmPrompt{
						Client:       a.azdClient,
						Message:      promptMessage,
						DefaultValue: to.Ptr(true),
						HelpMessage:  "Using an existing database will save you time and resources.",
					}
				}

				q.State["hasMessagingResource"] = hasMessagingResource

				return nil
			},
			AfterAsk: func(ctx context.Context, q *qna.Question, value any) error {
				hasMessagingResource := q.State["hasMessagingResource"].(bool)
				reuseMessaging, ok := value.(bool)

				if !hasMessagingResource || ok && !reuseMessaging {
					q.Next = []qna.QuestionReference{{Key: "choose-messaging-type"}}
				}

				return nil
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
					{Label: "Choose for me", Value: "messaging.servicebus"},
					{Label: "Azure Service Bus", Value: "messaging.servicebus"},
					{Label: "Azure Event Hubs", Value: "messaging.eventhubs"},
				},
			},
			Branches: map[any][]qna.QuestionReference{
				"messaging.eventhubs":  {{Key: "choose-messaging-resource"}},
				"messaging.servicebus": {{Key: "choose-messaging-resource"}},
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
							"unknown resource type for messaging: %s",
							a.scenarioData.MessagingType,
						)
					}

					p.ResourceType = resourceType.Type
					p.Kinds = resourceType.Kinds
					p.ResourceTypeDisplayName = resourceType.DisplayName
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
					"embeddings": {
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
					"audio": {
						Heading:     "Audio Model (For transcribing audio)",
						Description: "Used to convert audio files into text for further processing.",
						QuestionReference: qna.QuestionReference{
							Key: "start-choose-model",
							State: map[string]any{
								"modelSelectMessage": "Lets choose a audio model",
								"capabilities":       []string{"audio"},
							},
						},
					},
					"images": {
						Heading:     "Image Generation Model (For generating images)",
						Description: "Used to generate images based on text prompts.",
						QuestionReference: qna.QuestionReference{
							Key: "start-choose-model",
							State: map[string]any{
								"modelSelectMessage": "Lets choose a image generation model",
								"capabilities":       []string{"imageGenerations"},
							},
						},
					},
				}

				requiredModels := []string{"llm"}
				if slices.Contains(a.scenarioData.DataTypes, "structured-documents") ||
					slices.Contains(a.scenarioData.DataTypes, "unstructured-documents") {
					requiredModels = append(requiredModels, "embeddings")
				}

				if slices.Contains(a.scenarioData.DataTypes, "audio") {
					requiredModels = append(requiredModels, "audio")
				}

				if slices.Contains(a.scenarioData.DataTypes, "images") ||
					slices.Contains(a.scenarioData.DataTypes, "videos") {
					requiredModels = append(requiredModels, "images")
				}

				nextQuestions := []qna.QuestionReference{}

				fmt.Printf("  Based on your choices, you will need the following AI models:\n\n")
				for _, model := range requiredModels {
					if modelType, ok := allModelTypes[model]; ok {
						fmt.Printf("  - %s\n", output.WithBold("%s", modelType.Heading))
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
			Branches: map[any][]qna.QuestionReference{
				"guide-model": {{Key: "guide-model-select"}},
				"user-model":  {{Key: "user-model-select"}},
			},
			AfterAsk: func(ctx context.Context, q *qna.Question, value any) error {
				selectedValue := value.(string)
				if selectedValue == "choose-model" {
					capabilities, ok := q.State["capabilities"].([]string)
					if !ok {
						return nil
					}

					// If the user selected "choose-model", we need to set the model selection
					for key, value := range defaultModelMap {
						if slices.Contains(capabilities, key) {
							a.scenarioData.ModelSelections = append(
								a.scenarioData.ModelSelections,
								value,
							)
						}
					}
				}

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
			Branches: map[any][]qna.QuestionReference{
				"filter-model-capability": {{Key: "filter-model-capability"}},
				"filter-model-format":     {{Key: "filter-model-format"}},
				"filter-model-status":     {{Key: "filter-model-status"}},
				"filter-model-location":   {{Key: "filter-model-location"}},
			},
			Next: []qna.QuestionReference{{Key: "user-model-select"}},
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
	}, nil
}

func (a *startAction) generateResourceName(desiredName string) string {
	resourceMap := map[string]struct{}{}
	for _, resource := range a.composedResources {
		resourceMap[resource.Name] = struct{}{}
	}

	if _, exists := resourceMap[desiredName]; !exists {
		return desiredName
	}
	// If the desired name already exists, append a number (always 2 digits) to the name
	nextIndex := 1
	for {
		newName := fmt.Sprintf("%s-%02d", desiredName, nextIndex)
		if _, exists := resourceMap[newName]; !exists {
			return newName
		}
		nextIndex++
	}
}

func (a *startAction) generateServiceName(desiredName string) string {
	if _, exists := a.projectConfig.Services[desiredName]; !exists {
		return desiredName
	}
	// If the desired name already exists, append a number (always 2 digits) to the name
	nextIndex := 1
	for {
		newName := fmt.Sprintf("%s-%02d", desiredName, nextIndex)
		if _, exists := a.projectConfig.Services[newName]; !exists {
			return newName
		}
		nextIndex++
	}
}
